package jev

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	contracts "github.com/Herrscherd/herrscher-contracts"
)

const (
	reasonTimeout          = "timeout"
	reasonCanceled         = "canceled"
	reasonRedirect         = "redirect"
	reasonConnectionFailed = "connection failed"
	reasonInvalidSetting   = "invalid setting"
)

type prober struct {
	declared []contracts.Setting
	http     *http.Client
}

func (p prober) probe(ctx context.Context, access contracts.ProbeAccess) contracts.ProbeReport {
	opts, err := parseOptions(probeConfig(access, p.declared))
	if errors.Is(err, ErrMissingAPIKey) {
		return contracts.ProbeReport{
			State:  contracts.HealthDown,
			Detail: contracts.Notice{Code: contracts.CodeHealthEnvMissing, Args: []contracts.Arg{contracts.Text("Name", typesafeEnv)}},
			Remedy: contracts.Notice{Code: contracts.CodeHealthRemedyEnvRestart, Args: []contracts.Arg{contracts.Text("Name", typesafeEnv)}},
		}
	}
	var invalid *settingError
	if errors.As(err, &invalid) {
		return unreachable(contracts.HealthDown, reasonInvalidSetting+" "+invalid.key, 0)
	}
	if err != nil {
		return unreachable(contracts.HealthDown, reasonInvalidSetting, 0)
	}
	client := &jevClient{http: refusingClient(p.http, opts.timeout), endpoint: opts.endpoint, apiKey: opts.apiKey, model: opts.model}
	started := time.Now()
	err = client.ask(ctx, compactionState{History: []historyEntry{}}, make(map[string]question))
	return verdict(err, time.Since(started))
}

func probeConfig(access contracts.ProbeAccess, declared []contracts.Setting) contracts.PluginConfig {
	values := make(map[string]string, len(declared))
	for _, setting := range declared {
		value := access.Config.Get(setting.Key)
		if value == "" && setting.Env != "" && access.Getenv != nil {
			value = access.Getenv(setting.Env)
		}
		if value != "" {
			values[setting.Key] = value
		}
	}
	return contracts.PluginConfig{Settings: values}
}

func verdict(err error, latency time.Duration) contracts.ProbeReport {
	var refused *statusError
	switch {
	case err == nil, errors.Is(err, ErrJevMalformed):
		return connected(latency)
	case errors.As(err, &refused):
		return statusVerdict(refused.status, latency)
	case errors.Is(err, ErrJevRedirected):
		return unreachable(contracts.HealthDown, reasonRedirect, latency)
	case isTimeout(err):
		return unreachable(contracts.HealthDegraded, reasonTimeout, latency)
	case errors.Is(err, context.Canceled):
		return unreachable(contracts.HealthDegraded, reasonCanceled, latency)
	}
	return unreachable(contracts.HealthDown, reasonConnectionFailed, latency)
}

func statusVerdict(status int, latency time.Duration) contracts.ProbeReport {
	switch {
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return contracts.ProbeReport{State: contracts.HealthDown, Latency: latency, Detail: contracts.Notice{Code: contracts.CodeHealthCredentialsRefused}}
	case status == http.StatusTooManyRequests:
		return contracts.ProbeReport{State: contracts.HealthDegraded, Latency: latency, Detail: contracts.Notice{Code: contracts.CodeHealthRateLimited}}
	case status >= http.StatusInternalServerError:
		return unreachable(contracts.HealthDegraded, httpReason(status), latency)
	case status == http.StatusNotFound:
		return unreachable(contracts.HealthDown, httpReason(status), latency)
	}
	return connected(latency)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}

func httpReason(status int) string { return "http " + strconv.Itoa(status) }

func connected(latency time.Duration) contracts.ProbeReport {
	return contracts.ProbeReport{State: contracts.HealthOK, Latency: latency, Detail: contracts.Notice{Code: contracts.CodeHealthConnected}}
}

func unreachable(state contracts.HealthState, reason string, latency time.Duration) contracts.ProbeReport {
	return contracts.ProbeReport{
		State:   state,
		Latency: latency,
		Detail:  contracts.Notice{Code: contracts.CodeHealthUnreachable, Args: []contracts.Arg{contracts.Text("Reason", reason)}},
	}
}
