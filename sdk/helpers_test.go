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
