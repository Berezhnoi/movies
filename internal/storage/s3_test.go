package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"homework/internal/service"
)

// sdkError builds the error chain the SDK returns for an HTTP response:
// OperationError -> ResponseError (status) -> API error (code).
func sdkError(status int, code string) error {
	var cause error = errors.New("no API error")
	if code != "" {
		cause = &smithy.GenericAPIError{Code: code, Message: "test"}
	}
	return &smithy.OperationError{ServiceID: "S3", OperationName: "GetObject", Err: &awshttp.ResponseError{
		ResponseError: &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
			Err:      cause,
		},
	}}
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error // nil means the error is passed through unmapped
	}{
		{name: "missing key", err: sdkError(http.StatusNotFound, "NoSuchKey"), want: service.ErrObjectNotFound},
		{name: "missing bucket", err: sdkError(http.StatusNotFound, "NoSuchBucket"), want: service.ErrObjectNotFound},
		{name: "HEAD 404", err: sdkError(http.StatusNotFound, "NotFound"), want: service.ErrObjectNotFound},
		{name: "invalid range", err: sdkError(http.StatusRequestedRangeNotSatisfiable, "InvalidRange"), want: service.ErrRangeNotSatisfiable},
		{name: "416 without code", err: sdkError(http.StatusRequestedRangeNotSatisfiable, ""), want: service.ErrRangeNotSatisfiable},
		{name: "server error", err: sdkError(http.StatusInternalServerError, "InternalError"), want: service.ErrStorageUnavailable},
		{name: "transport error", err: fmt.Errorf("dial tcp: connection refused"), want: service.ErrStorageUnavailable},
		{name: "access denied", err: sdkError(http.StatusForbidden, "AccessDenied")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.err)
			if tt.want == nil {
				if got != tt.err {
					t.Errorf("mapError() = %v, want the error unchanged", got)
				}
				return
			}
			if !errors.Is(got, tt.want) {
				t.Errorf("mapError() = %v, want it to wrap %v", got, tt.want)
			}
		})
	}

	for _, ctxErr := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := mapError(fmt.Errorf("get: %w", ctxErr)); !errors.Is(got, ctxErr) || errors.Is(got, service.ErrStorageUnavailable) {
			t.Errorf("mapError(%v) = %v, want the context error passed through", ctxErr, got)
		}
	}
}
