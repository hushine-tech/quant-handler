package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	cerrors "github.com/hushine-tech/golang-lib/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const runtimeDependencyTestMessage = "Python module 'google.cloud' is not available"

func runtimeDependencyTestDetails(code string) map[string]string {
	return map[string]string{
		"code":                    code,
		"module":                  "google.cloud",
		"runtime_profile":         "platform-python-3.13",
		"runtime_profile_version": "1.0.0",
		"image_build_id":          "build-1",
		"message":                 runtimeDependencyTestMessage,
	}
}

func runtimeDependencyTestError(grpcCode codes.Code, dependencyCode string) error {
	common := cerrors.FromGRPCStatus(status.New(grpcCode, runtimeDependencyTestMessage)).WithDetails(runtimeDependencyTestDetails(dependencyCode))
	return cerrors.ToGRPCStatusWithCode(common, grpcCode).Err()
}

func TestRuntimeDependencyErrorFromGRPCPreservesAllowlistedDetails(t *testing.T) {
	want := &runtimeDependencyHTTPError{
		Code:                  "STRATEGY_DEPENDENCY_UNAVAILABLE",
		Module:                "google.cloud",
		RuntimeProfile:        "platform-python-3.13",
		RuntimeProfileVersion: "1.0.0",
		ImageBuildID:          "build-1",
		Message:               runtimeDependencyTestMessage,
	}

	got, ok := runtimeDependencyErrorFromGRPC(runtimeDependencyTestError(codes.FailedPrecondition, want.Code))
	if !ok {
		t.Fatal("dependency error was not recognized")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime error = %#v, want %#v", got, want)
	}
}

func TestRuntimeDependencyErrorFromGRPCHandlesClientInterceptorCommonError(t *testing.T) {
	raw := runtimeDependencyTestError(codes.FailedPrecondition, "STRATEGY_DEPENDENCY_UNAVAILABLE")
	converted := cerrors.FromGRPCStatus(status.Convert(raw))
	got, ok := runtimeDependencyErrorFromGRPC(converted)
	if !ok || got.Code != "STRATEGY_DEPENDENCY_UNAVAILABLE" || got.Message != runtimeDependencyTestMessage {
		t.Fatalf("converted error = %#v, ok=%v", got, ok)
	}
}

func TestRuntimeDependencyErrorFromGRPCAcceptsExactlyStableCodes(t *testing.T) {
	stable := []string{
		"UNSUPPORTED_STRATEGY_DEPENDENCY",
		"STRATEGY_DEPENDENCY_UNAVAILABLE",
		"STRATEGY_IMPORT_FAILED",
		"RUNTIME_DEPENDENCY_PROFILE_INVALID",
		"RUNTIME_DEPENDENCY_PROFILE_MISMATCH",
	}
	for _, code := range stable {
		t.Run(code, func(t *testing.T) {
			got, ok := runtimeDependencyErrorFromGRPC(runtimeDependencyTestError(codes.FailedPrecondition, code))
			if !ok || got.Code != code {
				t.Fatalf("got = %#v, ok=%v", got, ok)
			}
		})
	}
	if got, ok := runtimeDependencyErrorFromGRPC(runtimeDependencyTestError(codes.Internal, "SOME_INTERNAL_FAILURE")); ok || got != nil {
		t.Fatalf("unknown code accepted: %#v", got)
	}
}

func TestRuntimeDependencyErrorFromGRPCRejectsUnknownOrUnsafeDetails(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "unknown field", mutate: func(details map[string]string) { details["traceback"] = "/private/worker.py: secret" }},
		{name: "multiline message", mutate: func(details map[string]string) { details["message"] = "safe\nTraceback: /private/worker.py" }},
		{name: "invalid module", mutate: func(details map[string]string) { details["module"] = "../../private/worker.py" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := runtimeDependencyTestDetails("STRATEGY_DEPENDENCY_UNAVAILABLE")
			tt.mutate(details)
			common := cerrors.FromGRPCStatus(status.New(codes.Internal, runtimeDependencyTestMessage)).WithDetails(details)
			err := cerrors.ToGRPCStatusWithCode(common, codes.Internal).Err()
			if got, ok := runtimeDependencyErrorFromGRPC(err); ok || got != nil {
				t.Fatalf("unsafe details accepted: %#v", got)
			}
		})
	}
}

func TestWriteRuntimeDependencyErrorEmitsSafeEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	if !writeRuntimeDependencyError(rec, runtimeDependencyTestError(codes.FailedPrecondition, "STRATEGY_DEPENDENCY_UNAVAILABLE")) {
		t.Fatal("structured error was not written")
	}
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPreconditionFailed)
	}
	var body struct {
		Error        string                      `json:"error"`
		RuntimeError *runtimeDependencyHTTPError `json:"runtime_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != runtimeDependencyTestMessage || body.RuntimeError == nil || body.RuntimeError.Message != body.Error {
		t.Fatalf("response = %+v", body)
	}
	if contains(rec.Body.String(), "10000") || contains(rec.Body.String(), "StringValue") || contains(rec.Body.String(), "traceback") {
		t.Fatalf("response leaks transport detail: %s", rec.Body.String())
	}
}
