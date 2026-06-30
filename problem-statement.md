Customer: Hologen — contact: Eimantas Gecas (Slack)
Impact Type: Capability
Links: Freshdesk #25277 · Slack thread

Customer Problem

The customer needs to identify Cold objects in a CAIOS bucket (objects to evaluate for access-tier/cost purposes). The supported way to do this is an inventory report that includes the LastAccessedDate field, which CAIOS documents as the way to "derive the access tier (Hot, Warm, or Cold) of the objects."

The customer manages their infrastructure as code with Terraform. CAIOS inventory configuration does support LastAccessedDate, but they cannot configure it through Terraform today:

The AWS provider's aws_s3_bucket_inventory resource rejects LastAccessedDate, because that field is not in AWS's allowed optional_fields enum (it is a CoreWeave extension beyond standard S3 inventory).

The CoreWeave provider has no equivalent inventory resource to fall back to.

The customer's own words (Slack):

"We tried setting LastAccessedDate in OptionalFields. But the aws terraform provider rejects that field on the aws_s3_bucket_inventory. And your terraform provider does not have a coreweave_object_storage_bucket_inventory resource :grimacing:"

Customer Requirement

Add a native coreweave_object_storage_bucket_inventory resource to the CoreWeave Terraform provider that manages CAIOS bucket inventory configurations and supports the full CoreWeave field set, with optional_fields accepting the complete CAIOS superset (critically, LastAccessedDate) rather than mirroring the AWS provider's restrictive validation.

For consistency and easy adoption, the resource should follow the naming and schema conventions of the existing coreweave_object_storage_bucket_* resource family (_bucket, _bucket_policy, _bucket_settings, _bucket_lifecycle_configuration), and ideally mirror the attribute shape of aws_s3_bucket_inventory so the customer can migrate their current AWS-provider config with minimal rework.

The resource should map cleanly to the underlying S3-compatible inventory configuration API (PutBucketInventoryConfiguration / Get / List / Delete) and expose all documented configuration options:

Resource attribute

Accepted values

name / id (inventory config ID)

string

enabled (IsEnabled)

true / false

included_object_versions

All / Latest

schedule.frequency

Daily / Weekly

filter.prefix

source-object prefix (optional)

destination.bucket

destination bucket (may equal source bucket)

destination.format

CSV, TSV, JSON, ORC, Parquet

destination.prefix

report output prefix (optional)

optional_fields

any of: Size, LastModifiedDate, IsMultipartUploaded, EncryptionStatus, StorageClass, ChecksumAlgorithm, LastAccessedDate, ETag

Always-present output fields (BucketName, ObjectKey, and VersionID / IsLatest / IsDeleteMarker when included_object_versions = All) require no schema flags but should be documented.

What is it blocking

The customer cannot configure CAIOS inventory with LastAccessedDate in their Terraform workflow, which blocks them from producing the Cold-object listing they need through IaC. Today they must either drop to the S3 API / aws s3api CLI or use a Terraform escape hatch (null_resource + local-exec), neither of which gives proper state tracking, drift detection, or clean update/destroy — the value they expect from managing this as code.

Business impact: without the LastAccessedDate inventory listing, Hologen cannot drive their access-tier / cost-optimization workflow — that field is the documented input for deriving Hot/Warm/Cold tier and for any downstream coreweave_object_storage_bucket_lifecycle_configuration rules they would apply to cold data.

Acceptance criteria

coreweave_object_storage_bucket_inventory exists in the provider with full CRUD lifecycle mapped to the CAIOS inventory configuration API.

optional_fields accepts the complete CoreWeave superset and explicitly accepts LastAccessedDate (does not reject it the way aws_s3_bucket_inventory does).

Supports all documented options: formats (CSV/TSV/JSON/ORC/Parquet), Daily/Weekly schedules, All/Latest versions, source filter.prefix, destination bucket + prefix, and same-bucket destination.

terraform plan shows no drift after apply; updating optional_fields or schedule applies cleanly; destroy removes the configuration.

Provider docs and the CAIOS inventory Terraform example are updated to use the native resource.

Supporting context

CAIOS inventory feature and field reference: About Inventory Reporting · Configure inventory reporting

LastAccessedDate is documented as the field used to derive Hot/Warm/Cold access tier, which is exactly the customer's use case.

Existing provider resources to match for parity: coreweave_object_storage_bucket, _bucket_policy, _bucket_settings, _bucket_lifecycle_configuration.

Interim unblock available today: configure inventory via aws s3api put-bucket-inventory-configuration (accepts LastAccessedDate); this CFR is for the durable, IaC-native fix.

Related CFRs: CFR-129 (Hologen cold-data retirement lifecycle), CFR-114 (default inventory config for all buckets), CFR-132 (per-object tier visibility).

