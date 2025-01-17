// Copyright 2019-present Open Networking Foundation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package device

import (
	"context"
	"github.com/atomix/atomix-go-client/pkg/client/map"
	"github.com/atomix/atomix-go-client/pkg/client/primitive"
	"github.com/atomix/atomix-go-client/pkg/client/session"
	"github.com/atomix/atomix-go-local/pkg/atomix/local"
	"github.com/atomix/atomix-go-node/pkg/atomix"
	"github.com/atomix/atomix-go-node/pkg/atomix/registry"
	"github.com/gogo/protobuf/proto"
	deviceapi "github.com/onosproject/onos-topo/api/device"
	"github.com/onosproject/onos-topo/pkg/util"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"io"
	"net"
	"time"
)

// NewAtomixStore returns a new persistent Store
func NewAtomixStore() (Store, error) {
	client, err := util.GetAtomixClient()
	if err != nil {
		return nil, err
	}

	group, err := client.GetGroup(context.Background(), util.GetAtomixRaftGroup())
	if err != nil {
		return nil, err
	}

	devices, err := group.GetMap(context.Background(), "devices", session.WithTimeout(30*time.Second))
	if err != nil {
		return nil, err
	}

	return &atomixStore{
		devices: devices,
		closer:  devices,
	}, nil
}

// NewLocalStore returns a new local device store
func NewLocalStore() (Store, error) {
	node, conn := startLocalNode()
	name := primitive.Name{
		Namespace: "local",
		Name:      "devices",
	}

	devices, err := _map.New(context.Background(), name, []*grpc.ClientConn{conn})
	if err != nil {
		return nil, err
	}

	return &atomixStore{
		devices: devices,
		closer:  &nodeCloser{node},
	}, nil
}

// startLocalNode starts a single local node
func startLocalNode() (*atomix.Node, *grpc.ClientConn) {
	lis := bufconn.Listen(1024 * 1024)
	node := local.NewNode(lis, registry.Registry)
	_ = node.Start()

	dialer := func(ctx context.Context, address string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.DialContext(context.Background(), "devices", grpc.WithContextDialer(dialer), grpc.WithInsecure())
	if err != nil {
		panic("Failed to dial devices")
	}
	return node, conn
}

type nodeCloser struct {
	node *atomix.Node
}

func (c *nodeCloser) Close() error {
	return c.node.Stop()
}

// Store stores topology information
type Store interface {
	io.Closer

	// Load loads a device from the store
	Load(deviceID deviceapi.ID) (*deviceapi.Device, error)

	// Store stores a device in the store
	Store(*deviceapi.Device) error

	// Delete deletes a device from the store
	Delete(*deviceapi.Device) error

	// List streams devices to the given channel
	List(chan<- *deviceapi.Device) error

	// Watch streams device events to the given channel
	Watch(chan<- *Event) error
}

// atomixStore is the device implementation of the Store
type atomixStore struct {
	devices _map.Map
	closer  io.Closer
}

func (s *atomixStore) Load(deviceID deviceapi.ID) (*deviceapi.Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	entry, err := s.devices.Get(ctx, string(deviceID))
	if err != nil {
		return nil, err
	} else if entry == nil {
		return nil, nil
	}
	return decodeDevice(entry)
}

func (s *atomixStore) Store(device *deviceapi.Device) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	bytes, err := proto.Marshal(device)
	if err != nil {
		return err
	}

	// Put the device in the map using an optimistic lock if this is an update
	var entry *_map.Entry
	if device.Revision == 0 {
		entry, err = s.devices.Put(ctx, string(device.ID), bytes)
	} else {
		entry, err = s.devices.Put(ctx, string(device.ID), bytes, _map.IfVersion(int64(device.Revision)))
	}

	if err != nil {
		return err
	}

	// Update the device metadata
	device.Revision = deviceapi.Revision(entry.Version)
	return err
}

func (s *atomixStore) Delete(device *deviceapi.Device) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if device.Revision > 0 {
		_, err := s.devices.Remove(ctx, string(device.ID), _map.IfVersion(int64(device.Revision)))
		return err
	}
	_, err := s.devices.Remove(ctx, string(device.ID))
	return err
}

func (s *atomixStore) List(ch chan<- *deviceapi.Device) error {
	mapCh := make(chan *_map.Entry)
	if err := s.devices.Entries(context.Background(), mapCh); err != nil {
		return err
	}

	go func() {
		defer close(ch)
		for entry := range mapCh {
			if device, err := decodeDevice(entry); err == nil {
				ch <- device
			}
		}
	}()
	return nil
}

func (s *atomixStore) Watch(ch chan<- *Event) error {
	mapCh := make(chan *_map.Event)
	if err := s.devices.Watch(context.Background(), mapCh, _map.WithReplay()); err != nil {
		return err
	}

	go func() {
		defer close(ch)
		for event := range mapCh {
			if device, err := decodeDevice(event.Entry); err == nil {
				ch <- &Event{
					Type:   EventType(event.Type),
					Device: device,
				}
			}
		}
	}()
	return nil
}

func (s *atomixStore) Close() error {
	_ = s.devices.Close()
	return s.closer.Close()
}

func decodeDevice(entry *_map.Entry) (*deviceapi.Device, error) {
	device := &deviceapi.Device{}
	if err := proto.Unmarshal(entry.Value, device); err != nil {
		return nil, err
	}
	device.ID = deviceapi.ID(entry.Key)
	device.Revision = deviceapi.Revision(entry.Version)
	return device, nil
}

// EventType provides the type for a device event
type EventType string

const (
	// EventNone is no event
	EventNone EventType = ""
	// EventInserted is inserted
	EventInserted EventType = "inserted"
	// EventUpdated is updated
	EventUpdated EventType = "updated"
	// EventRemoved is removed
	EventRemoved EventType = "removed"
)

// Event is a store event for a device
type Event struct {
	Type   EventType
	Device *deviceapi.Device
}
