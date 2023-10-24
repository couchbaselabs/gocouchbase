package gocb

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"google.golang.org/grpc/status"

	"github.com/couchbase/goprotostellar/genproto/kv_v1"
)

// Contains information only useful to protostellar
type kvOpManagerPs struct {
	parent *Collection

	err error

	span            RequestSpan
	documentID      string
	transcoder      Transcoder
	timeout         time.Duration
	bytes           []byte
	flags           uint32
	durabilityLevel *kv_v1.DurabilityLevel
	retryStrategy   RetryStrategy
	ctx             context.Context
	isIdempotent    bool

	operationName string
	createdTime   time.Time
	meter         *meterWrapper
}

func (m *kvOpManagerPs) getTimeout() time.Duration {
	if m.timeout > 0 {
		if m.durabilityLevel != nil && m.timeout < durabilityTimeoutFloor {
			m.timeout = durabilityTimeoutFloor
			logWarnf("Durable operation in use so timeout value coerced up to %s", m.timeout.String())
		}
		return m.timeout
	}

	defaultTimeout := m.parent.timeoutsConfig.KVTimeout
	if m.durabilityLevel != nil && *m.durabilityLevel > kv_v1.DurabilityLevel_DURABILITY_LEVEL_MAJORITY {
		defaultTimeout = m.parent.timeoutsConfig.KVDurableTimeout
	}

	if m.durabilityLevel != nil && *m.durabilityLevel > 0 && defaultTimeout < durabilityTimeoutFloor {
		defaultTimeout = durabilityTimeoutFloor
		logWarnf("Durable operation in user so timeout value coerced up to %s", defaultTimeout.String())
	}

	return defaultTimeout
}

func (m *kvOpManagerPs) SetDocumentID(id string) {
	m.documentID = id
}

func (m *kvOpManagerPs) SetTimeout(timeout time.Duration) {
	m.timeout = timeout
}

func (m *kvOpManagerPs) SetTranscoder(transcoder Transcoder) {
	if transcoder == nil {
		transcoder = m.parent.transcoder
	}
	m.transcoder = transcoder
}

func (m *kvOpManagerPs) SetValue(val interface{}) {
	if m.err != nil {
		return
	}
	if m.transcoder == nil {
		m.err = errors.New("expected a transcoder to be specified first")
		return
	}

	espan := m.parent.startKvOpTrace("request_encoding", m.span.Context(), true)
	defer espan.End()

	bytes, flags, err := m.transcoder.Encode(val)
	if err != nil {
		m.err = err
		return
	}

	m.bytes = bytes
	m.flags = flags
}

func (m *kvOpManagerPs) SetDuraOptions(level DurabilityLevel) {
	if level == DurabilityLevelUnknown {
		level = DurabilityLevelNone
	}

	m.durabilityLevel, m.err = level.toProtostellar()

	if level > DurabilityLevelNone {
		levelStr, err := level.toManagementAPI()
		if err != nil {
			logDebugf("Could not convert durability level to string: %v", err)
			return
		}
		m.span.SetAttribute(spanAttribDBDurability, levelStr)
	}
}

func (m *kvOpManagerPs) SetRetryStrategy(retryStrategy RetryStrategy) {
	strat := m.parent.retryStrategyWrapper.wrapped
	if retryStrategy != nil {
		strat = retryStrategy
	}
	m.retryStrategy = strat
}

func (m *kvOpManagerPs) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	m.ctx = ctx
}

func (m *kvOpManagerPs) SetIsIdempotent(idempotent bool) {
	m.isIdempotent = idempotent
}

func (m *kvOpManagerPs) Finish(noMetrics bool) {
	m.span.End()

	if !noMetrics {
		m.meter.ValueRecord(meterValueServiceKV, m.operationName, m.createdTime)
	}
}

func (m *kvOpManagerPs) TraceSpanContext() RequestSpanContext {
	return m.span.Context()
}

func (m *kvOpManagerPs) TraceSpan() RequestSpan {
	return m.span
}

func (m *kvOpManagerPs) DocumentID() string {
	return m.documentID
}

func (m *kvOpManagerPs) CollectionName() string {
	return m.parent.name()
}

func (m *kvOpManagerPs) ScopeName() string {
	return m.parent.ScopeName()
}

func (m *kvOpManagerPs) BucketName() string {
	return m.parent.bucketName()
}

func (m *kvOpManagerPs) ValueBytes() []byte {
	return m.bytes
}

func (m *kvOpManagerPs) ValueFlags() uint32 {
	return m.flags
}

func (m *kvOpManagerPs) Transcoder() Transcoder {
	return m.transcoder
}

func (m *kvOpManagerPs) DurabilityLevel() *kv_v1.DurabilityLevel {
	return m.durabilityLevel
}

func (m *kvOpManagerPs) RetryStrategy() RetryStrategy {
	return m.retryStrategy
}

func (m *kvOpManagerPs) IsIdempotent() bool {
	return m.isIdempotent
}

func (m *kvOpManagerPs) CheckReadyForOp() error {
	if m.err != nil {
		return m.err
	}

	if m.getTimeout() == 0 {
		return errors.New("op manager had no timeout specified")
	}

	return nil
}

func (m *kvOpManagerPs) Wrap(fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	ctx, cancel := context.WithTimeout(m.ctx, m.getTimeout())
	defer cancel()

	return m.WrapCtx(ctx, fn)
}

func (m *kvOpManagerPs) WrapCtx(ctx context.Context, fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	req := newRetriableRequestPS(m.operationName, m.IsIdempotent(), m.TraceSpanContext(), uuid.NewString()[:6], m.RetryStrategy(), fn)

	res, err := handleRetriableRequest(ctx, m.createdTime, m.parent.tracer, req, func(err error) RetryReason {
		if errors.Is(err, ErrDocumentLocked) {
			return KVLockedRetryReason
		} else if errors.Is(err, ErrServiceNotAvailable) {
			return ServiceNotAvailableRetryReason
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (m *kvOpManagerPs) EnhanceErrorStatus(st *status.Status, readOnly bool) error {
	return mapPsErrorStatusToGocbError(st, readOnly)
}

func (m *kvOpManagerPs) EnhanceErr(err error, readOnly bool) error {
	return mapPsErrorToGocbError(err, readOnly)
}

func newKvOpManagerPs(c *Collection, opName string, parentSpan RequestSpan) *kvOpManagerPs {
	var tracectx RequestSpanContext
	if parentSpan != nil {
		tracectx = parentSpan.Context()
	}

	span := c.startKvOpTrace(opName, tracectx, false)

	return &kvOpManagerPs{
		parent:        c,
		span:          span,
		operationName: opName,
		createdTime:   time.Now(),
		meter:         c.meter,
	}
}
