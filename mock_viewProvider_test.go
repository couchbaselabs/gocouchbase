// Code generated by mockery v1.0.0. DO NOT EDIT.

package gocb

import (
	context "context"

	gocbcore "github.com/couchbase/gocbcore/v10"
	mock "github.com/stretchr/testify/mock"
)

// mockViewProvider is an autogenerated mock type for the viewProvider type
type mockViewProvider struct {
	mock.Mock
}

// ViewQuery provides a mock function with given fields: ctx, opts
func (_m *mockViewProvider) ViewQuery(ctx context.Context, opts gocbcore.ViewQueryOptions) (viewRowReader, error) {
	ret := _m.Called(ctx, opts)

	var r0 viewRowReader
	if rf, ok := ret.Get(0).(func(context.Context, gocbcore.ViewQueryOptions) viewRowReader); ok {
		r0 = rf(ctx, opts)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(viewRowReader)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, gocbcore.ViewQueryOptions) error); ok {
		r1 = rf(ctx, opts)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}
