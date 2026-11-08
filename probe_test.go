package jev

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

func testProber(client *http.Client) prober {
	return prober{declared: settings(newMessages()), http: client}
}

func probeServer(t *testing.T, server http.Handler, access contracts.ProbeAccess) contracts.ProbeReport {
	t.Helper()
	tls := httptest.NewTLSServer(server)
	t.Cleanup(tls.Close)
	values := map[string]string{settingEndpoint: tls.URL}
	for key, value := range access.Config.Settings {
		values[key] = value
	}
	access.Config = contracts.PluginConfig{Settings: values}
	return testProber(tls.Client()).probe(t.Context(), access)
}

func keyed(key string) contracts.ProbeAccess {
	return contracts.ProbeAccess{Config: contracts.PluginConfig{Settings: map[string]string{settingAPIKey: key}}}
}

func reason(report contracts.ProbeReport) string {
	for _, arg := range report.Detail.Args {
		if text, isText := arg.Value().(string); isText && arg.Name() == "Reason" {
			return text
		}
	}
	return ""
}

func TestProbeWithoutAKeyIsDownAndNamesTheVariable(t *testing.T) {
	t.Parallel()
	for name, access := range map[string]contracts.ProbeAccess{
		"no getenv":    {},
		"empty getenv": {Getenv: func(string) string { return "" }},
	} {
		report := testProber(nil).probe(t.Context(), access)
		want := contracts.ProbeReport{
			State:  contracts.HealthDown,
			Detail: contracts.Notice{Code: contracts.CodeHealthEnvMissing, Args: []contracts.Arg{contracts.Text("Name", typesafeEnv)}},
			Remedy: contracts.Notice{Code: contracts.CodeHealthRemedyEnvRestart, Args: []contracts.Arg{contracts.Text("Name", typesafeEnv)}},
		}
		if report.State != want.State || report.Detail.Code != want.Detail.Code || report.Remedy.Code != want.Remedy.Code ||
			!slices.Equal(report.Detail.Args, want.Detail.Args) || !slices.Equal(report.Remedy.Args, want.Remedy.Args) {
			t.Errorf("%s: probe = %+v, want %+v", name, report, want)
		}
	}
}

func TestProbeReadsTheKeyFromConfigBeforeTheEnvironment(t *testing.T) {
	t.Parallel()
	env := func(name string) string {
		if name == typesafeEnv {
			return "env-key"
		}
		return ""
	}
	cases := map[string]struct {
		access contracts.ProbeAccess
		want   string
	}{
		"config wins":     {access: contracts.ProbeAccess{Getenv: env, Config: keyed("config-key").Config}, want: "Bearer config-key"},
		"env as fallback": {access: contracts.ProbeAccess{Getenv: env}, want: "Bearer env-key"},
	}
	for name, tc := range cases {
		jev := &fakeJev{}
		if report := probeServer(t, jev, tc.access); report.State != contracts.HealthOK {
			t.Errorf("%s: state = %v, want ok", name, report.State)
		}
		if !slices.Equal(jev.auth, []string{tc.want}) {
			t.Errorf("%s: authorization = %q, want %q", name, jev.auth, tc.want)
		}
	}
}

func TestProbeAsksNoQuestion(t *testing.T) {
	t.Parallel()
	jev := &fakeJev{}
	for _, deep := range []bool{false, true} {
		access := keyed("key")
		access.Deep = deep
		probeServer(t, jev, access)
	}
	if len(jev.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(jev.requests))
	}
	for _, request := range jev.requests {
		if len(request.Questions) != 0 || len(request.State.History) != 0 {
			t.Fatalf("request = %+v, want no question and no history", request)
		}
	}
}

func TestProbeMapsTheStatusOfJev(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		state  contracts.HealthState
		code   contracts.Code
		reason string
	}{
		{status: 0, state: contracts.HealthOK, code: contracts.CodeHealthConnected},
		{status: http.StatusBadRequest, state: contracts.HealthOK, code: contracts.CodeHealthConnected},
		{status: http.StatusUnauthorized, state: contracts.HealthDown, code: contracts.CodeHealthCredentialsRefused},
		{status: http.StatusForbidden, state: contracts.HealthDown, code: contracts.CodeHealthCredentialsRefused},
		{status: http.StatusNotFound, state: contracts.HealthDown, code: contracts.CodeHealthUnreachable, reason: "http 404"},
		{status: http.StatusTooManyRequests, state: contracts.HealthDegraded, code: contracts.CodeHealthRateLimited},
		{status: http.StatusServiceUnavailable, state: contracts.HealthDegraded, code: contracts.CodeHealthUnreachable, reason: "http 503"},
	}
	for _, tc := range cases {
		report := probeServer(t, &fakeJev{status: tc.status, echoAuth: true}, keyed("secret-key"))
		if report.State != tc.state || report.Detail.Code != tc.code || reason(report) != tc.reason {
			t.Errorf("status %d: probe = %v %v %q, want %v %v %q", tc.status, report.State, report.Detail.Code, reason(report), tc.state, tc.code, tc.reason)
		}
		if report.Latency <= 0 {
			t.Errorf("status %d: latency = %v, want measured", tc.status, report.Latency)
		}
	}
}

func TestProbeTimeoutIsDegraded(t *testing.T) {
	t.Parallel()
	hanging := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		<-r.Context().Done()
	})
	tls := httptest.NewTLSServer(hanging)
	t.Cleanup(tls.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	access := contracts.ProbeAccess{Config: contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingEndpoint: tls.URL}}}
	report := testProber(tls.Client()).probe(ctx, access)
	if report.State != contracts.HealthDegraded || report.Detail.Code != contracts.CodeHealthUnreachable || reason(report) != reasonTimeout {
		t.Fatalf("probe = %+v, want degraded timeout", report)
	}
}

func TestProbeUnansweredEndpointIsDown(t *testing.T) {
	t.Parallel()
	redirecting := http.RedirectHandler("https://example.invalid/", http.StatusFound)
	closed := httptest.NewTLSServer(http.NotFoundHandler())
	closed.Close()
	cases := map[string]struct {
		report contracts.ProbeReport
		reason string
	}{
		"redirect":          {report: probeServer(t, redirecting, keyed("key")), reason: reasonRedirect},
		"closed":            {report: testProber(closed.Client()).probe(t.Context(), contracts.ProbeAccess{Config: contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingEndpoint: closed.URL}}}), reason: reasonConnectionFailed},
		"plain http config": {report: testProber(nil).probe(t.Context(), contracts.ProbeAccess{Config: contracts.PluginConfig{Settings: map[string]string{settingAPIKey: "key", settingEndpoint: "http://jev.test"}}}), reason: "invalid setting endpoint"},
	}
	for name, tc := range cases {
		if tc.report.State != contracts.HealthDown || tc.report.Detail.Code != contracts.CodeHealthUnreachable || reason(tc.report) != tc.reason {
			t.Errorf("%s: probe = %+v, want down %q", name, tc.report, tc.reason)
		}
	}
}

func TestProbeNeverShowsTheKey(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusBadGateway} {
		report := probeServer(t, &fakeJev{status: status, echoAuth: true}, keyed("secret-key"))
		for _, notice := range []contracts.Notice{report.Detail, report.Remedy} {
			for _, arg := range notice.Args {
				if strings.Contains(fmt.Sprint(arg.Value()), "secret-key") {
					t.Errorf("status %d: %s = %v carries the key", status, arg.Name(), arg.Value())
				}
			}
		}
	}
}
