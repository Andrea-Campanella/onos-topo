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
	deviceapi "github.com/onosproject/onos-topo/api/device"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"io"
	log "k8s.io/klog"
	"net"
	"testing"
	"time"
)

func TestLocalServer(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	s := grpc.NewServer()

	store, err := NewLocalStore()
	assert.NoError(t, err)
	defer store.Close()
	defer s.Stop()

	deviceapi.RegisterDeviceServiceServer(s, &Server{
		deviceStore: store,
	})

	go func() {
		if err := s.Serve(lis); err != nil {
			panic("Server exited with error")
		}
	}()

	dialer := func(ctx context.Context, address string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(dialer), grpc.WithInsecure())
	if err != nil {
		panic("Failed to dial bufnet")
	}

	client := CreateDeviceServiceClient(conn)

	_, err = client.Get(context.Background(), &deviceapi.GetRequest{
		ID: deviceapi.ID("none"),
	})
	assert.Error(t, err, "device not found")

	_, err = client.Add(context.Background(), &deviceapi.AddRequest{
		Device: &deviceapi.Device{
			ID:      deviceapi.ID("foo"),
			Type:    "test",
			Address: "foo:1234",
			Version: "1.0.0",
		},
	})
	assert.Error(t, err, "device ID 'foo' is invalid")

	_, err = client.Add(context.Background(), &deviceapi.AddRequest{
		Device: &deviceapi.Device{
			ID:      deviceapi.ID("foobar"),
			Type:    "test",
			Address: "baz",
			Version: "1.0.0",
		},
	})
	assert.Error(t, err, "device address 'baz' is invalid")

	_, err = client.Add(context.Background(), &deviceapi.AddRequest{
		Device: &deviceapi.Device{
			ID:      deviceapi.ID("foobar"),
			Type:    "test",
			Address: "baz:1234",
			Version: "abc",
		},
	})
	assert.Error(t, err, "device version 'abc' is invalid")

	addResponse, err := client.Add(context.Background(), &deviceapi.AddRequest{
		Device: &deviceapi.Device{
			ID:      deviceapi.ID("device-foo"),
			Type:    "test",
			Address: "device-foo:1234",
			Version: "1.0.0",
		},
	})
	assert.NoError(t, err)
	assert.NotEqual(t, deviceapi.Revision(0), addResponse.Device.Revision)

	getResponse, err := client.Get(context.Background(), &deviceapi.GetRequest{
		ID: deviceapi.ID("device-foo"),
	})
	assert.NoError(t, err)
	assert.Equal(t, deviceapi.ID("device-foo"), getResponse.Device.ID)
	assert.Equal(t, addResponse.Device.Revision, getResponse.Device.Revision)
	assert.Equal(t, "device-foo:1234", getResponse.Device.Address)
	device := getResponse.Device
	protocolState := new(deviceapi.ProtocolState)
	protocolState.Protocol = deviceapi.Protocol_GNMI
	protocolState.ConnectivityState = deviceapi.ConnectivityState_REACHABLE
	protocolState.ChannelState = deviceapi.ChannelState_CONNECTED
	protocolState.ServiceState = deviceapi.ServiceState_AVAILABLE
	device.Protocols = append(device.Protocols, protocolState)
	updateResponse, errResponse := client.Update(context.Background(), &deviceapi.UpdateRequest{
		Device: device,
	})
	assert.NoError(t, errResponse)
	assert.Equal(t, deviceapi.ID("device-foo"), updateResponse.Device.ID)
	assert.Equal(t, "device-foo:1234", updateResponse.Device.Address)
	assert.Equal(t, deviceapi.Protocol_GNMI, updateResponse.Device.Protocols[0].Protocol)
	assert.Equal(t, deviceapi.ConnectivityState_REACHABLE, updateResponse.Device.Protocols[0].ConnectivityState)
	assert.Equal(t, deviceapi.ChannelState_CONNECTED, updateResponse.Device.Protocols[0].ChannelState)
	assert.Equal(t, deviceapi.ServiceState_AVAILABLE, updateResponse.Device.Protocols[0].ServiceState)

	list, err := client.List(context.Background(), &deviceapi.ListRequest{})
	assert.NoError(t, err)
	for {
		listResponse, err := list.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("list failed with error %v", err)
		}
		assert.Equal(t, deviceapi.ID("device-foo"), listResponse.Device.ID)
		assert.Equal(t, updateResponse.Device.Revision, listResponse.Device.Revision)
		assert.Equal(t, "device-foo:1234", listResponse.Device.Address)
	}

	subscribe, err := client.List(context.Background(), &deviceapi.ListRequest{
		Subscribe: true,
	})
	assert.NoError(t, err)

	eventCh := make(chan *deviceapi.ListResponse)
	go func() {
		for {
			subscribeResponse, err := subscribe.Recv()
			if err != nil {
				break
			}
			eventCh <- subscribeResponse
		}
	}()
	select {
	case listResponse := <-eventCh:
		assert.Equal(t, deviceapi.ListResponse_NONE, listResponse.Type)
		assert.Equal(t, deviceapi.ID("device-foo"), listResponse.Device.ID)
		assert.Equal(t, "device-foo:1234", listResponse.Device.Address)
	case <-time.After(1 * time.Second):
		log.Error("Expected Update Response")
		t.FailNow()
	}

	addResponse, err = client.Add(context.Background(), &deviceapi.AddRequest{
		Device: &deviceapi.Device{
			ID:      deviceapi.ID("device-bar"),
			Type:    "test",
			Address: "device-bar:1234",
			Version: "1.0.0",
		},
	})
	assert.NoError(t, err)
	assert.Equal(t, deviceapi.ID("device-bar"), addResponse.Device.ID)
	assert.Equal(t, "device-bar:1234", addResponse.Device.Address)
	assert.NotEqual(t, deviceapi.Revision(0), addResponse.Device.Revision)

	select {
	case listResponse := <-eventCh:
		assert.Equal(t, deviceapi.ListResponse_ADDED, listResponse.Type)
		assert.Equal(t, deviceapi.ID("device-bar"), listResponse.Device.ID)
		assert.Equal(t, "device-bar:1234", listResponse.Device.Address)
	case <-time.After(1 * time.Second):
		log.Error("Expected Update Response")
		t.FailNow()
	}
	_, err = client.Remove(context.Background(), &deviceapi.RemoveRequest{
		Device: updateResponse.Device,
	})
	assert.NoError(t, err)

	select {
	case listResponse := <-eventCh:
		assert.Equal(t, deviceapi.ListResponse_REMOVED, listResponse.Type)
		assert.Equal(t, deviceapi.ID("device-foo"), listResponse.Device.ID)
		assert.Equal(t, "device-foo:1234", listResponse.Device.Address)
	case <-time.After(1 * time.Second):
		log.Error("Expected Update Response")
		t.FailNow()
	}

	_, err = client.Add(context.Background(), &deviceapi.AddRequest{
		Device: &deviceapi.Device{
			ID:      deviceapi.ID("good"),
			Type:    "test",
			Address: "10.11.12.13:1234",
			Version: "1.0.0",
		},
	})
	assert.NoError(t, err, "device should be good")
}
