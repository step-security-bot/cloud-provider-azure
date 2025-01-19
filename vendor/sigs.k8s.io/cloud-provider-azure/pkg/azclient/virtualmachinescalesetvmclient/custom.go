/*
Copyright 2023 The Kubernetes Authors.

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

package virtualmachinescalesetvmclient

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armcompute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"k8s.io/klog/v2"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/metrics"
	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/utils"
)

// Update updates a VirtualMachine.
func (client *Client) Update(ctx context.Context, resourceGroupName string, VMScaleSetName string, instanceID string, parameters armcompute.VirtualMachineScaleSetVM) (result *armcompute.VirtualMachineScaleSetVM, err error) {
	metricsCtx := metrics.BeginARMRequest(client.subscriptionID, resourceGroupName, "VirtualMachineScaleSetVM", "update")
	defer func() { metricsCtx.Observe(ctx, err) }()
	resp, err := utils.NewPollerWrapper(client.VirtualMachineScaleSetVMsClient.BeginUpdate(ctx, resourceGroupName, VMScaleSetName, instanceID, parameters, nil)).WaitforPollerResp(ctx)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return &resp.VirtualMachineScaleSetVM, nil
	}
	return nil, nil
}

// UpdateAsync updates a VirtualMachine.
func UpdateAsync(ctx context.Context, client *Client, resourceGroupName string, VMScaleSetName string, instanceID string, parameters armcompute.VirtualMachineScaleSetVM) (*utils.PollerWrapper[armcompute.VirtualMachineScaleSetVMsClientUpdateResponse], error) {
	poller, err := client.VirtualMachineScaleSetVMsClient.BeginUpdate(ctx, resourceGroupName, VMScaleSetName, instanceID, parameters, nil)
	if err != nil {
		return nil, err
	}
	pollerWraper := utils.NewPollerWrapper(poller, err)

	return pollerWraper, nil
}

// PutResourcesResponse defines the response for PutResources.
type PutResourcesResponse struct {
	Error error
}

func UpdateVMsInBatch(ctx context.Context, client *Client, resourceGroupName string, VMScaleSetName string, instances map[string]armcompute.VirtualMachineScaleSetVM, batchSize int) error {
	if len(instances) == 0 {
		return nil
	}

	if batchSize <= 0 {
		klog.V(4).Infof("PutResourcesInBatches: batch size %d, put resources in sequence", batchSize)
		batchSize = 1
	}

	if batchSize > len(instances) {
		klog.V(4).Infof("PutResourcesInBatches: batch size %d, but the number of the resources is %d", batchSize, len(instances))
		batchSize = len(instances)
	}

	metricsCtx := metrics.BeginARMRequest(client.subscriptionID, resourceGroupName, "VirtualMachineScaleSetVM", fmt.Sprintf("updateinbatch_%d", len(instances)))
	defer func() { metricsCtx.Observe(ctx, nil) }()

	wg := sync.WaitGroup{}
	rateLimiter := make(chan struct{}, batchSize)
	var responseLock, pollersLock sync.Mutex
	pollers := make(map[string]*utils.PollerWrapper[armcompute.VirtualMachineScaleSetVMsClientUpdateResponse])
	responses := make(map[string]*PutResourcesResponse)
	for instanceID, vm := range instances {
		rateLimiter <- struct{}{}
		wg.Add(1)
		go func(resourceID string, vm armcompute.VirtualMachineScaleSetVM) {
			defer wg.Done()
			defer func() { <-rateLimiter }()
			poller, err := UpdateAsync(ctx, client, resourceGroupName, VMScaleSetName, instanceID, vm)
			if err != nil {
				responseLock.Lock()
				responses[resourceID] = &PutResourcesResponse{
					Error: err,
				}
				responseLock.Unlock()
				return
			}
			pollersLock.Lock()
			pollers[resourceID] = poller
			pollersLock.Unlock()
		}(instanceID, vm)
	}
	wg.Wait()
	close(rateLimiter)
	klog.V(4).Infof("begin to wait async")

	waitAsync(ctx, pollers, responses)

	for _, response := range responses {
		if response.Error != nil {
			return response.Error
		}
	}

	return nil
}

func waitAsync(ctx context.Context, pollers map[string]*utils.PollerWrapper[armcompute.VirtualMachineScaleSetVMsClientUpdateResponse], previousResponses map[string]*PutResourcesResponse) {
	wg := sync.WaitGroup{}
	var responseLock sync.Mutex
	for resourceID, poller := range pollers {
		wg.Add(1)
		go func(resourceID string, poller *utils.PollerWrapper[armcompute.VirtualMachineScaleSetVMsClientUpdateResponse]) {
			defer wg.Done()
			_, err := poller.WaitforPollerResp(ctx)
			if err != nil {
				klog.V(5).Infof("Received error in WaitforPollerResp: '%s',", err.Error())
				responseLock.Lock()
				previousResponses[resourceID] = &PutResourcesResponse{
					Error: err,
				}
				responseLock.Unlock()
			}
			klog.V(4).Infof("instance %s finish", resourceID)

		}(resourceID, poller)
	}
	wg.Wait()
}

// List gets a list of VirtualMachineScaleSetVM in the resource group.
func (client *Client) List(ctx context.Context, resourceGroupName string, parentResourceName string) (result []*armcompute.VirtualMachineScaleSetVM, rerr error) {
	pager := client.VirtualMachineScaleSetVMsClient.NewListPager(resourceGroupName, parentResourceName, nil)
	for pager.More() {
		nextResult, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, nextResult.Value...)
	}
	return result, nil
}

// GetInstanceView gets the instance view of the VirtualMachineScaleSetVM.
func (client *Client) GetInstanceView(ctx context.Context, resourceGroupName string, vmScaleSetName string, instanceID string) (*armcompute.VirtualMachineScaleSetVMInstanceView, error) {
	resp, err := client.VirtualMachineScaleSetVMsClient.GetInstanceView(ctx, resourceGroupName, vmScaleSetName, instanceID, nil)
	if err != nil {
		return nil, err
	}
	return &resp.VirtualMachineScaleSetVMInstanceView, nil
}

// List gets a list of VirtualMachineScaleSetVM in the resource group.
func (client *Client) ListVMInstanceView(ctx context.Context, resourceGroupName string, parentResourceName string) (result []*armcompute.VirtualMachineScaleSetVM, rerr error) {
	pager := client.VirtualMachineScaleSetVMsClient.NewListPager(resourceGroupName, parentResourceName, &armcompute.VirtualMachineScaleSetVMsClientListOptions{
		Expand: to.Ptr(string(armcompute.InstanceViewTypesInstanceView)),
	})
	for pager.More() {
		nextResult, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, nextResult.Value...)
	}
	return result, nil
}
