// /*
// Copyright The Kubernetes Authors.
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
// */

// Code generated by client-gen. DO NOT EDIT.
package secretclient

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	armkeyvault "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"

	"sigs.k8s.io/cloud-provider-azure/pkg/azclient/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var vaultClient *armkeyvault.VaultsClient
var parentResource *armkeyvault.Vault

func init() {
	additionalTestCases = func() {
		When("creation requests are raised", func() {
			It("should not return error", func(ctx context.Context) {
				resourceName = "akscitkeyvaulttest"
				newResource, err := realClient.CreateOrUpdate(ctx, resourceGroupName, *parentResource.Name, resourceName, armkeyvault.SecretCreateOrUpdateParameters{
					Properties: &armkeyvault.SecretProperties{
						Value: to.Ptr("test"),
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(newResource).NotTo(BeNil())
				Expect(*newResource.Name).To(Equal(resourceName))
			})
		})
	}

	beforeAllFunc = func(ctx context.Context) {
		vaultClientFactory, err := armkeyvault.NewClientFactory(subscriptionID, recorder.TokenCredential(), &arm.ClientOptions{
			ClientOptions: policy.ClientOptions{
				Transport: recorder.HTTPClient(),
			},
		})
		Expect(err).NotTo(HaveOccurred())
		vaultClient = vaultClientFactory.NewVaultsClient()
		vaultName = "akscitsecretparevault"
		resp, err := utils.NewPollerWrapper(vaultClient.BeginCreateOrUpdate(ctx, resourceGroupName, vaultName, armkeyvault.VaultCreateOrUpdateParameters{
			Location: to.Ptr("eastus"),
			Properties: &armkeyvault.VaultProperties{
				EnabledForDeployment:         to.Ptr(true),
				EnabledForDiskEncryption:     to.Ptr(true),
				EnabledForTemplateDeployment: to.Ptr(true),
				PublicNetworkAccess:          to.Ptr("Enabled"),
				SKU: &armkeyvault.SKU{
					Name:   to.Ptr(armkeyvault.SKUNameStandard),
					Family: to.Ptr(armkeyvault.SKUFamilyA),
				},
				TenantID: to.Ptr(recorder.TenantID()),
				AccessPolicies: []*armkeyvault.AccessPolicyEntry{
					{
						ObjectID: to.Ptr("00000000-0000-0000-0000-000000000000"),
						Permissions: &armkeyvault.Permissions{
							Certificates: []*armkeyvault.CertificatePermissions{
								to.Ptr(armkeyvault.CertificatePermissionsGet),
								to.Ptr(armkeyvault.CertificatePermissionsList),
								to.Ptr(armkeyvault.CertificatePermissionsDelete),
								to.Ptr(armkeyvault.CertificatePermissionsCreate),
								to.Ptr(armkeyvault.CertificatePermissionsImport),
								to.Ptr(armkeyvault.CertificatePermissionsUpdate),
								to.Ptr(armkeyvault.CertificatePermissionsManagecontacts),
								to.Ptr(armkeyvault.CertificatePermissionsGetissuers),
								to.Ptr(armkeyvault.CertificatePermissionsListissuers),
								to.Ptr(armkeyvault.CertificatePermissionsSetissuers),
								to.Ptr(armkeyvault.CertificatePermissionsDeleteissuers),
								to.Ptr(armkeyvault.CertificatePermissionsManageissuers),
								to.Ptr(armkeyvault.CertificatePermissionsRecover),
								to.Ptr(armkeyvault.CertificatePermissionsPurge)},
							Keys: []*armkeyvault.KeyPermissions{
								to.Ptr(armkeyvault.KeyPermissionsEncrypt),
								to.Ptr(armkeyvault.KeyPermissionsDecrypt),
								to.Ptr(armkeyvault.KeyPermissionsWrapKey),
								to.Ptr(armkeyvault.KeyPermissionsUnwrapKey),
								to.Ptr(armkeyvault.KeyPermissionsSign),
								to.Ptr(armkeyvault.KeyPermissionsVerify),
								to.Ptr(armkeyvault.KeyPermissionsGet),
								to.Ptr(armkeyvault.KeyPermissionsList),
								to.Ptr(armkeyvault.KeyPermissionsCreate),
								to.Ptr(armkeyvault.KeyPermissionsUpdate),
								to.Ptr(armkeyvault.KeyPermissionsImport),
								to.Ptr(armkeyvault.KeyPermissionsDelete),
								to.Ptr(armkeyvault.KeyPermissionsBackup),
								to.Ptr(armkeyvault.KeyPermissionsRestore),
								to.Ptr(armkeyvault.KeyPermissionsRecover),
								to.Ptr(armkeyvault.KeyPermissionsPurge)},
							Secrets: []*armkeyvault.SecretPermissions{
								to.Ptr(armkeyvault.SecretPermissionsGet),
								to.Ptr(armkeyvault.SecretPermissionsList),
								to.Ptr(armkeyvault.SecretPermissionsSet),
								to.Ptr(armkeyvault.SecretPermissionsDelete),
								to.Ptr(armkeyvault.SecretPermissionsBackup),
								to.Ptr(armkeyvault.SecretPermissionsRestore),
								to.Ptr(armkeyvault.SecretPermissionsRecover),
								to.Ptr(armkeyvault.SecretPermissionsPurge)},
						},
						TenantID: to.Ptr(recorder.TenantID()),
					}},
			},
		}, nil)).WaitforPollerResp(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp).NotTo(BeNil())
		parentResource = &resp.Vault
		Expect(*resp.Vault.Name).To(Equal(vaultName))
	}
	afterAllFunc = func(ctx context.Context) {
		_, err = vaultClient.Delete(ctx, resourceGroupName, *parentResource.Name, nil)
		Expect(err).NotTo(HaveOccurred())
		_, err := vaultClient.BeginPurgeDeleted(ctx, *parentResource.Name, "eastus", nil)
		Expect(err).NotTo(HaveOccurred())
	}
}
