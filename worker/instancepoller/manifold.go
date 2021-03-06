// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package instancepoller

import (
	"github.com/juju/clock"
	"github.com/juju/errors"
	"gopkg.in/juju/names.v3"
	"gopkg.in/juju/worker.v1"
	"gopkg.in/juju/worker.v1/dependency"

	"github.com/juju/juju/api/base"
	"github.com/juju/juju/api/instancepoller"
	"github.com/juju/juju/core/watcher"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/worker/common"
)

// Logger represents the methods used by the worker to log details.
type Logger interface {
	Tracef(string, ...interface{})
	Debugf(string, ...interface{})
	Infof(string, ...interface{})
	Warningf(string, ...interface{})
	Errorf(string, ...interface{})
}

// facadeShim wraps an instancepoller API instance and allows us to provide
// methods that return interfaces which we can easily mock in our tests.
type facadeShim struct {
	api *instancepoller.API
}

func (s facadeShim) Machine(tag names.MachineTag) (Machine, error) { return s.api.Machine(tag) }
func (s facadeShim) WatchModelMachines() (watcher.StringsWatcher, error) {
	return s.api.WatchModelMachines()
}

// ManifoldConfig describes the resources used by the instancepoller worker.
type ManifoldConfig struct {
	APICallerName string
	ClockName     string
	EnvironName   string
	Logger        Logger

	NewCredentialValidatorFacade func(base.APICaller) (common.CredentialAPI, error)
}

func (config ManifoldConfig) start(context dependency.Context) (worker.Worker, error) {
	var clock clock.Clock
	if err := context.Get(config.ClockName, &clock); err != nil {
		return nil, errors.Trace(err)
	}
	var environ environs.Environ
	if err := context.Get(config.EnvironName, &environ); err != nil {
		return nil, errors.Trace(err)
	}

	var apiCaller base.APICaller
	if err := context.Get(config.APICallerName, &apiCaller); err != nil {
		return nil, errors.Trace(err)
	}

	credentialAPI, err := config.NewCredentialValidatorFacade(apiCaller)
	if err != nil {
		return nil, errors.Trace(err)
	}

	w, err := NewWorker(Config{
		Clock: clock,
		Facade: facadeShim{
			api: instancepoller.NewAPI(apiCaller),
		},
		Environ:       environ,
		Logger:        config.Logger,
		CredentialAPI: credentialAPI,
	})
	if err != nil {
		return nil, errors.Trace(err)
	}
	return w, nil
}

// Manifold returns a Manifold that encapsulates the instancepoller worker.
func Manifold(config ManifoldConfig) dependency.Manifold {
	return dependency.Manifold{
		Inputs: []string{
			config.APICallerName,
			config.EnvironName,
			config.ClockName,
		},
		Start: config.start,
	}
}
