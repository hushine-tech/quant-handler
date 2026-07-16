package app

import (
	"errors"
	"net/http"
	"strings"
	"unicode"

	cerrors "github.com/hushine-tech/golang-lib/pkg/errors"
	"google.golang.org/grpc/status"
)

const maxRuntimeDependencyMessageBytes = 1024

type runtimeDependencyHTTPError struct {
	Code                  string `json:"code"`
	Module                string `json:"module"`
	RuntimeProfile        string `json:"runtime_profile"`
	RuntimeProfileVersion string `json:"runtime_profile_version"`
	ImageBuildID          string `json:"image_build_id"`
	Message               string `json:"message"`
}

func runtimeDependencyErrorFromGRPC(err error) (*runtimeDependencyHTTPError, bool) {
	details := runtimeDependencyDetails(err)
	if len(details) == 0 {
		return nil, false
	}
	for key := range details {
		if !isRuntimeDependencyDetailField(key) {
			return nil, false
		}
	}
	code := details["code"]
	if !isStableRuntimeDependencyCode(code) {
		return nil, false
	}
	module := details["module"]
	profile := details["runtime_profile"]
	profileVersion := details["runtime_profile_version"]
	imageBuildID := details["image_build_id"]
	message := details["message"]
	if !validPythonModuleName(module) ||
		!validRuntimeDependencyToken(profile, 128) ||
		!validRuntimeDependencyToken(profileVersion, 64) ||
		!validRuntimeDependencyToken(imageBuildID, 256) ||
		!validRuntimeDependencyMessage(message) {
		return nil, false
	}
	return &runtimeDependencyHTTPError{
		Code:                  code,
		Module:                module,
		RuntimeProfile:        profile,
		RuntimeProfileVersion: profileVersion,
		ImageBuildID:          imageBuildID,
		Message:               message,
	}, true
}

func isStableRuntimeDependencyCode(code string) bool {
	switch code {
	case "UNSUPPORTED_STRATEGY_DEPENDENCY",
		"STRATEGY_DEPENDENCY_UNAVAILABLE",
		"STRATEGY_IMPORT_FAILED",
		"RUNTIME_DEPENDENCY_PROFILE_INVALID",
		"RUNTIME_DEPENDENCY_PROFILE_MISMATCH":
		return true
	default:
		return false
	}
}

func isRuntimeDependencyDetailField(field string) bool {
	switch field {
	case "code", "module", "runtime_profile", "runtime_profile_version", "image_build_id", "message":
		return true
	default:
		return false
	}
}

func runtimeDependencyDetails(err error) map[string]string {
	if err == nil {
		return nil
	}
	var common *cerrors.CommonError
	if errors.As(err, &common) {
		return common.DetailsCopy()
	}
	if grpcStatus, ok := status.FromError(err); ok {
		if decoded := cerrors.FromGRPCStatus(grpcStatus); decoded != nil {
			return decoded.DetailsCopy()
		}
	}
	return nil
}

func validPythonModuleName(module string) bool {
	if module == "" {
		return true
	}
	if len(module) > 255 {
		return false
	}
	for _, part := range strings.Split(module, ".") {
		if part == "" {
			return false
		}
		for i, r := range part {
			if i == 0 {
				if r != '_' && !unicode.IsLetter(r) {
					return false
				}
				continue
			}
			if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
	}
	return true
}

func validRuntimeDependencyToken(value string, maxBytes int) bool {
	if value == "" {
		return true
	}
	if len(value) > maxBytes {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		switch r {
		case '.', '_', '-', '+', ':':
			continue
		default:
			return false
		}
	}
	return true
}

func validRuntimeDependencyMessage(message string) bool {
	if strings.TrimSpace(message) == "" || len(message) > maxRuntimeDependencyMessageBytes {
		return false
	}
	if strings.Contains(strings.ToLower(message), "traceback") {
		return false
	}
	for _, r := range message {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func cloneRuntimeDependencyHTTPError(in *runtimeDependencyHTTPError) *runtimeDependencyHTTPError {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func writeRuntimeDependencyError(w http.ResponseWriter, err error) bool {
	runtimeErr, ok := runtimeDependencyErrorFromGRPC(err)
	if !ok {
		return false
	}
	httpStatus, _ := grpcToHTTP(err)
	writeJSON(w, httpStatus, struct {
		Error        string                      `json:"error"`
		RuntimeError *runtimeDependencyHTTPError `json:"runtime_error"`
	}{
		Error:        runtimeErr.Message,
		RuntimeError: runtimeErr,
	})
	return true
}
