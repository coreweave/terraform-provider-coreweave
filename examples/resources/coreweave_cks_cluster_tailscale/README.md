# Overview
In order to provision a new CKS cluster with Tailscale enabled from the start we need to get around a circular dependency.
The CKS cluster needs the Tailscale Client ID from the federated identity resource but the federated identity resource also needs the CKS cluster ID to
properly setup its Issuer URL and Audience. The example resources are setup so that you can run terraform apply consecutive times and reach a steady state without having to pass
any vars via flags.

# Usage
Sample usage is shown below. Note that TailWeave will not be able to successfully bootstrap itself until the second terraform apply has occurred and updated the values of the federated identity resource.

- (1) On the first run all resources are created. The Tailscale federated identity Issuer and Audience hav a `TODO` placeholder is their URL. The Service Account OIDC Issuer URL is written to a .tfvars file to be used on subsequent runs.
- (2) The second run updates the Issuer URL and Audience using the CKS cluster ID obtained from the first run.
- (3) The subsequent run reaches a steady state.
```
(1)
$ ./devtf -chdir=examples/resources/coreweave_cks_cluster_tailscale apply
...
tailscale_federated_identity.default: Creating...
coreweave_networking_vpc.default: Creating...
tailscale_federated_identity.default: Creation complete after 1s [id=Ts8tuGbCEM11CNTRL-kQD1jXuruV11CNTRL]
coreweave_networking_vpc.default: Creation complete after 6s [id=d9e93a25-8596-4e0f-8cc0-2ed12a684539]
coreweave_cks_cluster.default: Creating...
coreweave_cks_cluster.default: Still creating... [00m10s elapsed]
coreweave_cks_cluster.default: Still creating... [00m20s elapsed]
coreweave_cks_cluster.default: Still creating... [00m30s elapsed]
coreweave_cks_cluster.default: Still creating... [00m40s elapsed]
coreweave_cks_cluster.default: Still creating... [00m50s elapsed]
coreweave_cks_cluster.default: Still creating... [01m00s elapsed]
coreweave_cks_cluster.default: Still creating... [01m10s elapsed]
coreweave_cks_cluster.default: Still creating... [01m20s elapsed]
coreweave_cks_cluster.default: Still creating... [01m30s elapsed]
coreweave_cks_cluster.default: Still creating... [01m40s elapsed]
coreweave_cks_cluster.default: Still creating... [01m50s elapsed]
coreweave_cks_cluster.default: Still creating... [02m00s elapsed]
coreweave_cks_cluster.default: Still creating... [02m10s elapsed]
coreweave_cks_cluster.default: Still creating... [02m20s elapsed]
coreweave_cks_cluster.default: Still creating... [02m30s elapsed]
coreweave_cks_cluster.default: Still creating... [02m40s elapsed]
coreweave_cks_cluster.default: Creation complete after 2m49s [id=66329d4a-1260-433d-9232-f2eaff185151]
terraform_data.cluster_tfvars: Creating...
terraform_data.cluster_tfvars: Provisioning with 'local-exec'...
terraform_data.cluster_tfvars (local-exec): Executing: ["/bin/sh" "-c" "printf 'service_account_oidc_issuer_url = \"%s\"\\n' 'https://oidc.cks.coreweave.com/id/66329d4a-1260-433d-9232-f2eaff185151' > './terraform.tfvars'"]
terraform_data.cluster_tfvars: Creation complete after 0s [id=7095416b-d7f3-943f-12ed-dc47f757764f]

Apply complete! Resources: 4 added, 0 changed, 0 destroyed.

Outputs:

cluster_id = "66329d4a-1260-433d-9232-f2eaff185151"
service_account_oidc_issuer_url = "https://oidc.cks.coreweave.com/id/66329d4a-1260-433d-9232-f2eaff185151"


(2)
$ ./devtf -chdir=examples/resources/coreweave_cks_cluster_tailscale plan
...
coreweave_networking_vpc.default: Refreshing state... [id=d9e93a25-8596-4e0f-8cc0-2ed12a684539]
tailscale_federated_identity.default: Refreshing state... [id=Ts8tuGbCEM11CNTRL-kQD1jXuruV11CNTRL]
coreweave_cks_cluster.default: Refreshing state... [id=66329d4a-1260-433d-9232-f2eaff185151]
terraform_data.cluster_tfvars: Refreshing state... [id=7095416b-d7f3-943f-12ed-dc47f757764f]

Terraform used the selected providers to generate the following execution plan. Resource actions are indicated with the following symbols:
  ~ update in-place

Terraform will perform the following actions:

  # tailscale_federated_identity.default will be updated in-place
  ~ resource "tailscale_federated_identity" "default" {
      ~ audience           = "https://oidc.cks.coreweave.com/id/TODO" -> "https://oidc.cks.coreweave.com/id/66329d4a-1260-433d-9232-f2eaff185151"
        id                 = "Ts8tuGbCEM11CNTRL-kQD1jXuruV11CNTRL"
      ~ issuer             = "https://oidc.cks.coreweave.com/id/TODO" -> "https://oidc.cks.coreweave.com/id/66329d4a-1260-433d-9232-f2eaff185151"
        tags               = [
            "tag:coreweave",
        ]
        # (7 unchanged attributes hidden)
    }

Plan: 0 to add, 1 to change, 0 to destroy.

Do you want to perform these actions?
  Terraform will perform the actions described above.
  Only 'yes' will be accepted to approve.

  Enter a value: yes

tailscale_federated_identity.default: Modifying... [id=Ts8tuGbCEM11CNTRL-kQD1jXuruV11CNTRL]
tailscale_federated_identity.default: Modifications complete after 1s [id=Ts8tuGbCEM11CNTRL-kQD1jXuruV11CNTRL]

Apply complete! Resources: 0 added, 1 changed, 0 destroyed.

Outputs:

cluster_id = "66329d4a-1260-433d-9232-f2eaff185151"
service_account_oidc_issuer_url = "https://oidc.cks.coreweave.com/id/66329d4a-1260-433d-9232-f2eaff185151"


(3)
$ ./devtf -chdir=examples/resources/coreweave_cks_cluster_tailscale plan
...
tailscale_federated_identity.default: Refreshing state... [id=Ts8tuGbCEM11CNTRL-kL6pTDB27X11CNTRL]
coreweave_networking_vpc.default: Refreshing state... [id=57c35bd0-ea71-44af-a9be-b67a9e8f076a]
coreweave_cks_cluster.default: Refreshing state... [id=ed63bb10-bfa9-4477-835a-7a287d638d9a]
terraform_data.cluster_tfvars: Refreshing state... [id=511a8476-cd7b-4bd5-31dc-0a5267693fca]

No changes. Your infrastructure matches the configuration.

Terraform has compared your real infrastructure against your configuration and found no differences, so no changes are needed.
```
