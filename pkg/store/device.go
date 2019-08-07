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

package store

import (
	"context"
	"github.com/atomix/atomix-go-client/pkg/client/map_"
	"github.com/atomix/atomix-go-client/pkg/client/session"
	"github.com/gogo/protobuf/proto"
	deviceproto "github.com/onosproject/onos-topo/pkg/northbound/proto"
	"time"
)

// NewAtomixDeviceStore returns a new persistent DeviceStore
func NewAtomixDeviceStore() (DeviceStore, error) {
	client, err := getAtomixClient()
	if err != nil {
		return nil, err
	}

	group, err := client.GetGroup(context.Background(), getAtomixRaftGroup())
	if err != nil {
		return nil, err
	}

	devices, err := group.GetMap(context.Background(), "devices", session.WithTimeout(30*time.Second))
	if err != nil {
		return nil, err
	}

	return &atomixDeviceStore{
		devices: devices,
	}, nil
}

// DeviceStore stores topology information
type DeviceStore interface {
	// Load loads a device from the store
	Load(deviceID string) (*deviceproto.Device, error)

	// Store stores a device in the store
	Store(*deviceproto.Device) error

	// Delete deletes a device from the store
	Delete(*deviceproto.Device) error

	// List streams devices to the given channel
	List(chan<- *deviceproto.Device) error

	// Watch streams device events to the given channel
	Watch(chan<- *DeviceEvent) error
}

// atomixDeviceStore is the device implementation of the DeviceStore
type atomixDeviceStore struct {
	devices map_.Map
}

func (s *atomixDeviceStore) Load(deviceID string) (*deviceproto.Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	kv, err := s.devices.Get(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	return decodeDevice(kv.Key, kv.Value, kv.Version)
}

func (s *atomixDeviceStore) Store(device *deviceproto.Device) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	bytes, err := proto.Marshal(device)
	if err != nil {
		return err
	}

	// Put the device in the map using an optimistic lock if this is an update
	var kv *map_.KeyValue
	if device.Metadata == nil || device.Metadata.Version == 0 {
		kv, err = s.devices.Put(ctx, device.Id, bytes)
	} else {
		kv, err = s.devices.Put(ctx, device.Id, bytes, map_.WithVersion(int64(device.Metadata.Version)))
	}

	if err != nil {
		return err
	}

	// Update the device metadata
	device.Metadata = &deviceproto.ObjectMetadata{
		Id:      device.Id,
		Version: uint64(kv.Version),
	}
	return err
}

func (s *atomixDeviceStore) Delete(device *deviceproto.Device) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if device.Metadata != nil && device.Metadata.Version > 0 {
		_, err := s.devices.Remove(ctx, device.Metadata.Id, map_.WithVersion(int64(device.Metadata.Version)))
		return err
	}
	_, err := s.devices.Remove(ctx, device.Id)
	return err
}

func (s *atomixDeviceStore) List(ch chan<- *deviceproto.Device) error {
	mapCh := make(chan *map_.KeyValue)
	if err := s.devices.Entries(context.Background(), mapCh); err != nil {
		return err
	}

	go func() {
		defer close(ch)
		for kv := range mapCh {
			if device, err := decodeDevice(kv.Key, kv.Value, kv.Version); err == nil {
				ch <- device
			}
		}
	}()
	return nil
}

func (s *atomixDeviceStore) Watch(ch chan<- *DeviceEvent) error {
	mapCh := make(chan *map_.MapEvent)
	if err := s.devices.Watch(context.Background(), mapCh, map_.WithReplay()); err != nil {
		return err
	}

	go func() {
		defer close(ch)
		for event := range mapCh {
			if device, err := decodeDevice(event.Key, event.Value, event.Version); err == nil {
				ch <- &DeviceEvent{
					Type:   DeviceEventType(event.Type),
					Device: device,
				}
			}
		}
	}()
	return nil
}

func decodeDevice(key string, value []byte, version int64) (*deviceproto.Device, error) {
	device := &deviceproto.Device{}
	if err := proto.Unmarshal(value, device); err != nil {
		return nil, err
	}
	device.Metadata = &deviceproto.ObjectMetadata{
		Id:      key,
		Version: uint64(version),
	}
	return device, nil
}

// DeviceEventType provides the type for a device event
type DeviceEventType string

const (
	EventNone      DeviceEventType = ""
	DeviceInserted DeviceEventType = "inserted"
	DeviceUpdated  DeviceEventType = "updated"
	DeviceRemoved  DeviceEventType = "removed"
)

// DeviceEvent is a store event for a device
type DeviceEvent struct {
	Type   DeviceEventType
	Device *deviceproto.Device
}