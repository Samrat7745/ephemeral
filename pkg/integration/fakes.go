// Copyright (c) 2021 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"github.com/Samrat7745/ephemeral/pkg/discovery/fsm"
	. "github.com/Samrat7745/ephemeral/pkg/types"

	mb "github.com/vardius/message-bus"
)

type FakeOutputReader struct {
	bus    mb.MessageBus
	doneCh chan struct{}
}

func (f *FakeOutputReader) Process() {
	f.bus.Subscribe(ServiceEventsTopic, func(e interface{}) error {
		ev := e.(*fsm.Event)
		if ev.Name == GameDone {
			f.doneCh <- struct{}{}
		}
		return nil
	})
	return
}
