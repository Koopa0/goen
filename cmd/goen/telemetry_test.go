package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestTracingIncludesOpenTelemetryHTTP(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte("var handler = telemetry.HTTP(mux)")) {
		t.Fatal("newRouter must wrap the mux with telemetry.HTTP before chrome middleware")
	}
}

func TestProviderInstrumentationIsWired(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Clean(filepath.Join(root, "..", ".."))
	tests := []struct {
		file string
		want string
	}{
		{file: "internal/payment/stripe.go", want: `telemetry.BeginProvider(ctx, telemetry.ProviderStripe, "checkout.create")`},
		{file: "internal/invoice/ecpay.go", want: `telemetry.BeginProvider(ctx, telemetry.ProviderECPay, ecpayOperation(path))`},
		{file: "internal/email/sender.go", want: `telemetry.BeginProvider(ctx, telemetry.ProviderSMTP, "send")`},
	}
	for _, tt := range tests {
		//nolint:gosec // fixed paths under the module root in a source-wiring test
		src, err := os.ReadFile(filepath.Join(root, tt.file))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(src, []byte(tt.want)) {
			t.Fatalf("%s must contain %q", tt.file, tt.want)
		}
	}
}

func TestDiagnosticsRoutesStayOnAdminMux(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `"/admin/diagnostics/"`) {
		t.Fatal("diagnostics must mount under /admin/diagnostics, not the storefront root")
	}
	if strings.Contains(string(src), `mux.Handle("/debug/pprof/"`) {
		t.Fatal("pprof must not register on the public mux")
	}
}

func TestMainRegistersPoolAndOutboxTelemetry(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		"registerCommerceTelemetry(ctx, pool, adminPool)",
		"telemetry.RegisterOutboxCollector",
		"telemetry.CorrelatedHandler",
	} {
		if !bytes.Contains(src, []byte(needle)) {
			t.Fatalf("main.go must wire %q", needle)
		}
	}
}
