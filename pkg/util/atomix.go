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

package util

import (
	"github.com/atomix/atomix-go-client/pkg/client"
	"os"
)

const (
	atomixControllerEnv = "ATOMIX_CONTROLLER"
	atomixNamespaceEnv  = "ATOMIX_NAMESPACE"
	atomixAppEnv        = "ATOMIX_APP"
	atomixRaftGroup     = "ATOMIX_RAFT"
)

func getAtomixController() string {
	return os.Getenv(atomixControllerEnv)
}

func getAtomixNamespace() string {
	return os.Getenv(atomixNamespaceEnv)
}

func getAtomixApp() string {
	return os.Getenv(atomixAppEnv)
}

// GetAtomixRaftGroup get the Atomix Raft group
func GetAtomixRaftGroup() string {
	return os.Getenv(atomixRaftGroup)
}

// GetAtomixClient returns the Atomix client
func GetAtomixClient() (*client.Client, error) {
	opts := []client.Option{
		client.WithNamespace(getAtomixNamespace()),
		client.WithApplication(getAtomixApp()),
	}
	return client.NewClient(getAtomixController(), opts...)
}
