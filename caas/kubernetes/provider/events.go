// Copyright 2019 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provider

import (
	"github.com/juju/errors"

	core "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/juju/juju/core/watcher"
)

// Constants below are copied from "k8s.io/kubernetes/pkg/kubelet/events"
// to avoid introducing the huge dependency.
// Remove them once k8s.io/kubernetes added as a dependency.
const (
	// Container event reason list
	CreatedContainer        = "Created"
	StartedContainer        = "Started"
	FailedToCreateContainer = "Failed"
	FailedToStartContainer  = "Failed"
	KillingContainer        = "Killing"
	PreemptContainer        = "Preempting"
	BackOffStartContainer   = "BackOff"
	ExceededGracePeriod     = "ExceededGracePeriod"

	// Pod event reason list
	FailedToKillPod                = "FailedKillPod"
	FailedToCreatePodContainer     = "FailedCreatePodContainer"
	FailedToMakePodDataDirectories = "Failed"
	NetworkNotReady                = "NetworkNotReady"

	// Image event reason list
	PullingImage            = "Pulling"
	PulledImage             = "Pulled"
	FailedToPullImage       = "Failed"
	FailedToInspectImage    = "InspectFailed"
	ErrImageNeverPullPolicy = "ErrImageNeverPull"
	BackOffPullImage        = "BackOff"
)

func (k *kubernetesClient) getEvents(objName string, objKind string) ([]core.Event, error) {
	selector := fields.AndSelectors(
		fields.OneTermEqualSelector("involvedObject.name", objName),
		fields.OneTermEqualSelector("involvedObject.kind", objKind),
	).String()
	logger.Debugf("getting the latest event for %q", selector)
	eventList, err := k.client().CoreV1().Events(k.namespace).List(v1.ListOptions{
		IncludeUninitialized: true,
		FieldSelector:        selector,
	})
	if err != nil {
		return nil, errors.Trace(err)
	}
	return eventList.Items, nil
}

func (k *kubernetesClient) watchEvents(objName string, objKind string) (watcher.NotifyWatcher, error) {
	selector := fields.AndSelectors(
		fields.OneTermEqualSelector("involvedObject.name", objName),
		fields.OneTermEqualSelector("involvedObject.kind", objKind),
	).String()
	events := k.client().CoreV1().Events(k.namespace)
	w, err := events.Watch(v1.ListOptions{
		FieldSelector: selector,
		Watch:         true,
	})
	if err != nil {
		return nil, errors.Trace(err)
	}
	return k.newWatcher(w, objName, k.clock)
}
