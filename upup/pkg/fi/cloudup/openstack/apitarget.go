/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package openstack

import (
	"fmt"
	"k8s.io/kops/upup/pkg/fi"
)

type OpenstackAPITarget struct {
	Cloud OpenstackCloud
}

var _ fi.Target = &OpenstackAPITarget{}

func NewOpenstackAPITarget(cloud OpenstackCloud) *OpenstackAPITarget {
	return &OpenstackAPITarget{
		Cloud: cloud,
	}
}

func (t *OpenstackAPITarget) Finish(taskMap map[string]fi.Task) error {
	return nil
}

func (t *OpenstackAPITarget) ProcessDeletions() bool {
	return true
}

func (t *OpenstackAPITarget) GetIDForServerGroupName(sgName *string) (*string, error) {
	serverGroups, _ := t.Cloud.ListServerGroups()
	for _, serverGroup := range serverGroups {
		if serverGroup.Name == *sgName {
			return fi.String(serverGroup.ID), nil
		}
	}
	return nil, fmt.Errorf("ID not found for Server Group Name %s.", *sgName)
}
