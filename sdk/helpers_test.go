package sdk

import "testing"

func TestWithCleanQuery(t *testing.T) {
	resp := Allow(WithCleanQuery())
	if !resp.CleanQuery {
		t.Fatal("WithCleanQuery should set CleanQuery to true")
	}
	if !resp.Allow {
		t.Fatal("Allow should be true")
	}
}

func TestWithCleanQueryComposed(t *testing.T) {
	resp := Allow(
		WithHeader("X-Test", "value"),
		WithCleanQuery(),
		WithSpeedLimit(10, 5),
	)
	if !resp.CleanQuery {
		t.Fatal("CleanQuery should be true")
	}
	if resp.Headers["X-Test"] != "value" {
		t.Fatal("header should be set")
	}
	if resp.SpeedLimit == nil || resp.SpeedLimit.DownloadMbps != 10 {
		t.Fatal("speed limit should be set")
	}
}

func TestWithoutCleanQuery(t *testing.T) {
	resp := Allow(WithHeader("X-Test", "value"))
	if resp.CleanQuery {
		t.Fatal("CleanQuery should be false by default")
	}
}

func TestWithSpeedLimitGroupKey(t *testing.T) {
	resp := Allow(WithSpeedLimit(50, 25, "user-123"))
	if resp.SpeedLimit == nil {
		t.Fatal("speed limit should be set")
	}
	if resp.SpeedLimit.DownloadMbps != 50 {
		t.Fatalf("download = %v, want 50", resp.SpeedLimit.DownloadMbps)
	}
	if resp.SpeedLimit.UploadMbps != 25 {
		t.Fatalf("upload = %v, want 25", resp.SpeedLimit.UploadMbps)
	}
	if resp.SpeedLimit.GroupKey != "user-123" {
		t.Fatalf("group key = %q, want %q", resp.SpeedLimit.GroupKey, "user-123")
	}
}

func TestWithSpeedLimitNoGroupKey(t *testing.T) {
	resp := Allow(WithSpeedLimit(10, 5))
	if resp.SpeedLimit == nil {
		t.Fatal("speed limit should be set")
	}
	if resp.SpeedLimit.GroupKey != "" {
		t.Fatalf("group key should be empty, got %q", resp.SpeedLimit.GroupKey)
	}
}
