package gocb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/couchbase/gocbcore/v10"
	"github.com/couchbase/gocbcoreps"
)

type psConnectionMgr struct {
	host   string
	lock   sync.Mutex
	config *gocbcoreps.DialOptions
	agent  *gocbcoreps.RoutingClient

	timeouts     TimeoutsConfig
	tracer       RequestTracer
	meter        *meterWrapper
	defaultRetry RetryStrategy
}

func (c *psConnectionMgr) connect() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	client, err := gocbcoreps.Dial(c.host, c.config)
	if err != nil {
		return err
	}

	c.agent = client

	return nil
}

func (c *psConnectionMgr) openBucket(bucketName string) error {
	return nil
}

func (c *psConnectionMgr) buildConfig(cluster *Cluster) error {
	c.host = fmt.Sprintf("%s:%d", cluster.connSpec().Addresses[0].Host, cluster.connSpec().Addresses[0].Port)

	creds, err := cluster.authenticator().Credentials(AuthCredsRequest{})
	if err != nil {
		return err
	}

	logger := newZapLogger()

	c.config = &gocbcoreps.DialOptions{
		Username:           creds[0].Username,
		Password:           creds[0].Password,
		InsecureSkipVerify: cluster.securityConfig.TLSSkipVerify,
		ClientCertificate:  cluster.securityConfig.TLSRootCAs,
		Logger:             logger,
	}

	return nil
}

func (c *psConnectionMgr) getKvProvider(bucketName string) (kvProvider, error) {
	kv := c.agent.KvV1()
	return &kvProviderPs{client: kv}, nil
}

func (c *psConnectionMgr) getKvBulkProvider(bucketName string) (kvBulkProvider, error) {
	kv := c.agent.KvV1()
	return &kvBulkProviderPs{client: kv}, nil
}

func (c *psConnectionMgr) getKvCapabilitiesProvider(bucketName string) (kvCapabilityVerifier, error) {
	return &gocbcore.AgentInternal{}, ErrFeatureNotAvailable
}

func (c *psConnectionMgr) getViewProvider(bucketName string) (viewProvider, error) {
	return &viewProviderWrapper{}, ErrFeatureNotAvailable
}

func (c *psConnectionMgr) getQueryProvider() (queryProvider, error) {
	provider := c.agent.QueryV1()
	return &queryProviderPs{
		provider: provider,

		managerProvider: newPsOpManagerProvider(c.defaultRetry, c.tracer, c.timeouts.QueryTimeout, c.meter, meterValueServiceQuery),
	}, nil
}

func (c *psConnectionMgr) getQueryIndexProvider() (queryIndexProvider, error) {
	provider := c.agent.QueryAdminV1()
	return &queryIndexProviderPs{
		provider: provider,

		managerProvider: newPsOpManagerProvider(c.defaultRetry, c.tracer, c.timeouts.QueryTimeout, c.meter, meterValueServiceManagement),
	}, nil
}

func (c *psConnectionMgr) getSearchIndexProvider() (searchIndexProvider, error) {
	provider := c.agent.SearchAdminV1()
	return &searchIndexProviderPs{
		provider: provider,

		managerProvider: newPsOpManagerProvider(c.defaultRetry, c.tracer, c.timeouts.QueryTimeout, c.meter, meterValueServiceManagement),
	}, nil
}

func (c *psConnectionMgr) getCollectionsManagementProvider(bucketName string) (collectionsManagementProvider, error) {
	return &collectionsManagementProviderPs{
		provider:   c.agent.CollectionV1(),
		bucketName: bucketName,

		managerProvider: newPsOpManagerProvider(c.defaultRetry, c.tracer, c.timeouts.QueryTimeout, c.meter, meterValueServiceManagement),
	}, nil
}

func (c *psConnectionMgr) getBucketManagementProvider() (bucketManagementProvider, error) {
	return &bucketManagementProviderPs{
		provider: c.agent.BucketV1(),

		managerProvider: newPsOpManagerProvider(c.defaultRetry, c.tracer, c.timeouts.QueryTimeout, c.meter, meterValueServiceManagement),
	}, nil
}

func (c *psConnectionMgr) getAnalyticsProvider() (analyticsProvider, error) {
	return &analyticsProviderWrapper{}, ErrFeatureNotAvailable
}
func (c *psConnectionMgr) getSearchProvider() (searchProvider, error) {
	return &searchProviderPs{
		provider: c.agent.SearchV1(),

		managerProvider: newPsOpManagerProvider(c.defaultRetry, c.tracer, c.timeouts.QueryTimeout, c.meter, meterValueServiceSearch),
	}, nil
}
func (c *psConnectionMgr) getHTTPProvider(bucketName string) (httpProvider, error) {
	return &httpProviderWrapper{}, ErrFeatureNotAvailable
}
func (c *psConnectionMgr) getDiagnosticsProvider(bucketName string) (diagnosticsProvider, error) {
	return &diagnosticsProviderWrapper{}, ErrFeatureNotAvailable
}
func (c *psConnectionMgr) getWaitUntilReadyProvider(bucketName string) (waitUntilReadyProvider, error) {
	return &waitUntilReadyProviderPs{
		defaultRetryStrategy: c.defaultRetry,
		client:               c.agent,
	}, nil
}
func (c *psConnectionMgr) connection(bucketName string) (*gocbcore.Agent, error) {
	return &gocbcore.Agent{}, ErrFeatureNotAvailable
}
func (c *psConnectionMgr) close() error {
	return c.agent.Close()
}

type psOpManagerProvider struct {
	defaultRetryStrategy RetryStrategy
	tracer               RequestTracer
	defaultTimeout       time.Duration
	meter                *meterWrapper
	service              string
}

func newPsOpManagerProvider(retry RetryStrategy, tracer RequestTracer, timeout time.Duration, meter *meterWrapper,
	service string) *psOpManagerProvider {
	return &psOpManagerProvider{
		defaultRetryStrategy: retry,
		tracer:               tracer,
		defaultTimeout:       timeout,
		meter:                meter,
		service:              service,
	}
}

func (p *psOpManagerProvider) NewManager(parentSpan RequestSpan, opName string, attribs map[string]interface{}) *psOpManager {
	m := &psOpManager{
		defaultRetryStrategy: p.defaultRetryStrategy,
		tracer:               p.tracer,
		defaultTimeout:       p.defaultTimeout,
		meter:                p.meter,
		service:              p.service,
		createdTime:          time.Now(),

		span:   createSpan(p.tracer, parentSpan, opName, p.service),
		opName: opName,
	}

	for key, value := range attribs {
		m.span.SetAttribute(key, value)
	}

	return m
}

type psOpManager struct {
	defaultRetryStrategy RetryStrategy
	tracer               RequestTracer
	defaultTimeout       time.Duration
	meter                *meterWrapper
	service              string
	createdTime          time.Time

	span   RequestSpan
	opName string

	operationID   string
	retryStrategy RetryStrategy
	timeout       time.Duration
	ctx           context.Context
	isIdempotent  bool
}

func (m *psOpManager) SetTimeout(timeout time.Duration) {
	m.timeout = timeout
}

func (m *psOpManager) SetRetryStrategy(retryStrategy RetryStrategy) {
	strat := m.defaultRetryStrategy
	if retryStrategy != nil {
		strat = retryStrategy
	}
	m.retryStrategy = strat
}

func (m *psOpManager) SetContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	m.ctx = ctx
}

func (m *psOpManager) SetIsIdempotent(idempotent bool) {
	m.isIdempotent = idempotent
}

func (m *psOpManager) SetOperationID(id string) {
	m.operationID = id
}

func (m *psOpManager) NewSpan(name string) RequestSpan {
	return createSpan(m.tracer, m.span, name, m.service)
}

func (m *psOpManager) Finish(noMetrics bool) {
	m.span.End()

	if !noMetrics {
		m.meter.ValueRecord(m.service, m.opName, m.createdTime)
	}
}

func (m *psOpManager) TraceSpanContext() RequestSpanContext {
	return m.span.Context()
}

func (m *psOpManager) TraceSpan() RequestSpan {
	return m.span
}

func (m *psOpManager) RetryStrategy() RetryStrategy {
	return m.retryStrategy
}

func (m *psOpManager) IsIdempotent() bool {
	return m.isIdempotent
}

func (m *psOpManager) GetTimeout() time.Duration {
	if m.timeout > 0 {
		return m.timeout
	}

	return m.defaultTimeout
}

func (m *psOpManager) CheckReadyForOp() error {
	if m.GetTimeout() == 0 {
		return errors.New("op manager had no timeout specified")
	}

	if m.span == nil {
		return errors.New("op manager had no span specified")
	}

	return nil
}

func (m *psOpManager) ElapsedTime() time.Duration {
	return time.Since(m.createdTime)
}

func (m *psOpManager) OperationID() string {
	return m.operationID
}

func (m *psOpManager) Wrap(fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	ctx, cancel := context.WithTimeout(m.ctx, m.GetTimeout())
	defer cancel()

	return m.WrapCtx(ctx, fn)
}

func (m *psOpManager) WrapCtx(ctx context.Context, fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	req := newRetriableRequestPS(m.opName, m.IsIdempotent(), m.TraceSpanContext(), m.OperationID(), m.RetryStrategy(), fn)

	res, err := handleRetriableRequest(ctx, m.createdTime, m.tracer, req, func(err error) RetryReason {
		if errors.Is(err, ErrServiceNotAvailable) {
			return ServiceNotAvailableRetryReason
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}