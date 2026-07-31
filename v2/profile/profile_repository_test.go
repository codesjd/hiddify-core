package profile

import (
	"context"
	"testing"

	"github.com/hiddify/hiddify-core/v2/hcommon"
)

// These tests guard the not-found path in DeleteProfile/SetActiveProfile's
// default branch (no Id set on the request, and Name/Url doesn't match any
// stored profile). GetProfile returns (nil, error) in that case, and the two
// callers used to dereference the nil result directly - a nil-pointer panic.
// A profile name/URL that cannot plausibly exist is used so this passes
// against the current on-disk "data" database as-is, with no fixture setup.

func TestDeleteProfile_NotFoundByName_DoesNotPanic(t *testing.T) {
	s := &ProfileRepositoryServer{}
	req := &ProfileRequest{Name: "plan-026-does-not-exist"}

	resp, err := s.DeleteProfile(context.Background(), req)

	if err == nil {
		t.Fatal("expected a non-nil error for a not-found profile, got nil")
	}
	if resp == nil {
		t.Fatal("expected a non-nil response even on failure")
	}
	if resp.Code != hcommon.ResponseCode_FAILED {
		t.Fatalf("expected ResponseCode_FAILED, got %v", resp.Code)
	}
}

func TestSetActiveProfile_NotFoundByName_DoesNotPanic(t *testing.T) {
	s := &ProfileRepositoryServer{}
	req := &ProfileRequest{Name: "plan-026-does-not-exist"}

	resp, err := s.SetActiveProfile(context.Background(), req)

	if err == nil {
		t.Fatal("expected a non-nil error for a not-found profile, got nil")
	}
	if resp == nil {
		t.Fatal("expected a non-nil response even on failure")
	}
	if resp.Code != hcommon.ResponseCode_FAILED {
		t.Fatalf("expected ResponseCode_FAILED, got %v", resp.Code)
	}
}
