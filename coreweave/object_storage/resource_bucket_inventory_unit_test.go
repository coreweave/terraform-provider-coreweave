package objectstorage

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
)

// fullInventoryModel returns a fully-populated model exercising every field,
// including LastAccessedDate in optional_fields (the CoreWeave extension at the
// heart of CFR-178).
func fullInventoryModel() *BucketInventoryResourceModel {
	return &BucketInventoryResourceModel{
		Bucket:                 types.StringValue("source-bucket"),
		Name:                   types.StringValue("inv-config"),
		Enabled:                types.BoolValue(true),
		IncludedObjectVersions: types.StringValue("All"),
		OptionalFields: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("Size"),
			types.StringValue("LastAccessedDate"),
		}),
		Filter:   &InventoryFilterModel{Prefix: types.StringValue("logs/")},
		Schedule: &ScheduleModel{Frequency: types.StringValue("Daily")},
		Destination: &DestinationModel{
			Bucket: &BucketModel{
				BucketArn: types.StringValue("arn:aws:s3:::dest-bucket"),
				Format:    types.StringValue("CSV"),
				Prefix:    types.StringValue("reports/"),
				AccountId: types.StringValue("123456789012"),
			},
		},
	}
}

func TestExpandInventoryConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	got, diags := expandInventoryConfiguration(ctx, fullInventoryModel())
	require.False(t, diags.HasError(), "unexpected diagnostics: %v", diags)
	require.NotNil(t, got)

	require.Equal(t, "inv-config", aws.ToString(got.Id))
	require.True(t, aws.ToBool(got.IsEnabled))
	// Enum fields are compared as strings to avoid coupling to SDK constant names.
	require.Equal(t, "All", string(got.IncludedObjectVersions))

	require.NotNil(t, got.Schedule)
	require.Equal(t, "Daily", string(got.Schedule.Frequency))

	require.NotNil(t, got.Filter)
	require.Equal(t, "logs/", aws.ToString(got.Filter.Prefix))

	require.NotNil(t, got.Destination)
	require.NotNil(t, got.Destination.S3BucketDestination)
	dest := got.Destination.S3BucketDestination
	require.Equal(t, "arn:aws:s3:::dest-bucket", aws.ToString(dest.Bucket))
	require.Equal(t, "CSV", string(dest.Format))
	require.Equal(t, "reports/", aws.ToString(dest.Prefix))
	require.Equal(t, "123456789012", aws.ToString(dest.AccountId))

	// The core of CFR-178: LastAccessedDate must survive expand even though it is
	// not one of the AWS SDK's predefined InventoryOptionalField constants.
	gotFields := make([]string, 0, len(got.OptionalFields))
	for _, f := range got.OptionalFields {
		gotFields = append(gotFields, string(f))
	}
	require.ElementsMatch(t, []string{"Size", "LastAccessedDate"}, gotFields)
}

func TestExpandInventoryConfiguration_OmitsOptionalFieldsWhenNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	data := fullInventoryModel()
	data.OptionalFields = types.SetNull(types.StringType)
	data.Filter = nil

	got, diags := expandInventoryConfiguration(ctx, data)
	require.False(t, diags.HasError(), "unexpected diagnostics: %v", diags)
	require.NotNil(t, got)
	require.Nil(t, got.OptionalFields, "null optional_fields should not produce a slice")
	require.Nil(t, got.Filter, "absent filter should not produce a filter")
}

func TestFlattenInventoryConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	in := &s3types.InventoryConfiguration{
		Id:                     aws.String("inv-config"),
		IsEnabled:              aws.Bool(true),
		IncludedObjectVersions: s3types.InventoryIncludedObjectVersions("All"),
		Schedule:               &s3types.InventorySchedule{Frequency: s3types.InventoryFrequency("Weekly")},
		Filter:                 &s3types.InventoryFilter{Prefix: aws.String("logs/")},
		Destination: &s3types.InventoryDestination{
			S3BucketDestination: &s3types.InventoryS3BucketDestination{
				Bucket:    aws.String("arn:aws:s3:::dest-bucket"),
				Format:    s3types.InventoryFormat("Parquet"),
				Prefix:    aws.String("reports/"),
				AccountId: aws.String("123456789012"),
			},
		},
		OptionalFields: []s3types.InventoryOptionalField{"Size", "LastAccessedDate"},
	}

	// Bucket is not part of the API payload; simulate that it was already in state
	// and assert flatten preserves it.
	data := &BucketInventoryResourceModel{Bucket: types.StringValue("source-bucket")}
	diags := flattenInventoryConfiguration(ctx, in, data)
	require.False(t, diags.HasError(), "unexpected diagnostics: %v", diags)

	require.Equal(t, "source-bucket", data.Bucket.ValueString(), "bucket must be preserved")
	require.Equal(t, "inv-config", data.Name.ValueString())
	require.True(t, data.Enabled.ValueBool())
	require.Equal(t, "All", data.IncludedObjectVersions.ValueString())

	require.NotNil(t, data.Schedule)
	require.Equal(t, "Weekly", data.Schedule.Frequency.ValueString())

	require.NotNil(t, data.Filter)
	require.Equal(t, "logs/", data.Filter.Prefix.ValueString())

	require.NotNil(t, data.Destination)
	require.NotNil(t, data.Destination.Bucket)
	require.Equal(t, "arn:aws:s3:::dest-bucket", data.Destination.Bucket.BucketArn.ValueString())
	require.Equal(t, "Parquet", data.Destination.Bucket.Format.ValueString())
	require.Equal(t, "reports/", data.Destination.Bucket.Prefix.ValueString())
	require.Equal(t, "123456789012", data.Destination.Bucket.AccountId.ValueString())

	var fields []string
	require.False(t, data.OptionalFields.ElementsAs(ctx, &fields, false).HasError())
	require.ElementsMatch(t, []string{"Size", "LastAccessedDate"}, fields)
}

func TestFlattenInventoryConfiguration_EmptyOptionalFieldsIsNull(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	in := &s3types.InventoryConfiguration{
		Id:                     aws.String("inv-config"),
		IsEnabled:              aws.Bool(false),
		IncludedObjectVersions: s3types.InventoryIncludedObjectVersions("Latest"),
	}

	data := &BucketInventoryResourceModel{}
	diags := flattenInventoryConfiguration(ctx, in, data)
	require.False(t, diags.HasError(), "unexpected diagnostics: %v", diags)

	// Empty optional fields must round-trip to a null set (not an empty set) to
	// match a config that omits optional_fields, otherwise plan shows drift.
	require.True(t, data.OptionalFields.IsNull())
	require.Nil(t, data.Schedule)
	require.Nil(t, data.Filter)
	require.Nil(t, data.Destination)
}

// TestInventoryConfiguration_RoundTrip proves expand and flatten are inverses:
// model -> SDK -> model -> SDK should be stable, which is what keeps Terraform
// from reporting drift after apply. It compares the two SDK configs with the
// same equality check the create/update consistency poll uses.
func TestInventoryConfiguration_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	original := fullInventoryModel()

	sdk1, diags := expandInventoryConfiguration(ctx, original)
	require.False(t, diags.HasError(), "expand: %v", diags)

	roundTripped := &BucketInventoryResourceModel{Bucket: original.Bucket}
	diags = flattenInventoryConfiguration(ctx, sdk1, roundTripped)
	require.False(t, diags.HasError(), "flatten: %v", diags)

	sdk2, diags := expandInventoryConfiguration(ctx, roundTripped)
	require.False(t, diags.HasError(), "re-expand: %v", diags)

	require.True(t, eqInventoryConfiguration(*sdk1, *sdk2), "round trip changed the configuration")
}

// inventorySchema builds the resource schema so tests can exercise the real
// validators wired into it (rather than re-declaring them).
func inventorySchema(t *testing.T) schema.Schema {
	t.Helper()
	resp := &resource.SchemaResponse{}
	NewBucketInventoryResource().Schema(context.Background(), resource.SchemaRequest{}, resp)
	require.False(t, resp.Diagnostics.HasError(), "schema diagnostics: %v", resp.Diagnostics)
	return resp.Schema
}

// runStringValidators runs every validator attached to a string attribute and
// returns the aggregated diagnostics.
func runStringValidators(ctx context.Context, vals []validator.String, value types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, v := range vals {
		r := &validator.StringResponse{}
		v.ValidateString(ctx, validator.StringRequest{Path: path.Root("test"), ConfigValue: value}, r)
		diags.Append(r.Diagnostics...)
	}
	return diags
}

// runSetValidators runs every validator attached to a set attribute and returns
// the aggregated diagnostics.
func runSetValidators(ctx context.Context, vals []validator.Set, value types.Set) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, v := range vals {
		r := &validator.SetResponse{}
		v.ValidateSet(ctx, validator.SetRequest{Path: path.Root("test"), ConfigValue: value}, r)
		diags.Append(r.Diagnostics...)
	}
	return diags
}

// nestedStringValidators pulls the validators off a string attribute nested in a
// SingleNestedBlock (e.g. schedule.frequency, destination.format).
func nestedStringValidators(t *testing.T, sch schema.Schema, block, attribute string) []validator.String {
	t.Helper()
	b, ok := sch.Blocks[block].(schema.SingleNestedBlock)
	require.Truef(t, ok, "block %q is not a SingleNestedBlock", block)
	a, ok := b.Attributes[attribute].(schema.StringAttribute)
	require.Truef(t, ok, "%s.%s is not a StringAttribute", block, attribute)
	return a.StringValidators()
}

func TestInventorySchema_IncludedObjectVersionsValidator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	att, ok := inventorySchema(t).Attributes["included_object_versions"].(schema.StringAttribute)
	require.True(t, ok)
	vals := att.StringValidators()
	require.NotEmpty(t, vals, "included_object_versions should have validators")

	cases := map[string]struct {
		value   string
		wantErr bool
	}{
		"All is valid":         {"All", false},
		"Latest is valid":      {"Latest", false},
		"Current is rejected":  {"Current", true}, // AWS enum value, not the documented CAIOS value
		"arbitrary rejected":   {"Everything", true},
		"empty string rejected": {"", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags := runStringValidators(ctx, vals, types.StringValue(tc.value))
			require.Equal(t, tc.wantErr, diags.HasError(), "diags: %v", diags)
		})
	}
}

func TestInventorySchema_OptionalFieldsValidator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	att, ok := inventorySchema(t).Attributes["optional_fields"].(schema.SetAttribute)
	require.True(t, ok)
	vals := att.SetValidators()
	require.NotEmpty(t, vals, "optional_fields should have validators")

	setOf := func(elems ...string) types.Set {
		vs := make([]attr.Value, 0, len(elems))
		for _, e := range elems {
			vs = append(vs, types.StringValue(e))
		}
		return types.SetValueMust(types.StringType, vs)
	}

	cases := map[string]struct {
		value   types.Set
		wantErr bool
	}{
		// LastAccessedDate must be accepted — this is the crux of CFR-178.
		"LastAccessedDate accepted":       {setOf("LastAccessedDate"), false},
		"full superset accepted":          {setOf("Size", "LastModifiedDate", "LastAccessedDate", "StorageClass", "ETag", "IsMultipartUploaded", "EncryptionStatus", "ChecksumAlgorithm"), false},
		"unknown field rejected":          {setOf("Bogus"), true},
		"one invalid among valid rejected": {setOf("Size", "NotAField"), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags := runSetValidators(ctx, vals, tc.value)
			require.Equal(t, tc.wantErr, diags.HasError(), "diags: %v", diags)
		})
	}
}

func TestInventorySchema_ScheduleFrequencyValidator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	vals := nestedStringValidators(t, inventorySchema(t), "schedule", "frequency")
	require.NotEmpty(t, vals, "schedule.frequency should have validators")

	cases := map[string]struct {
		value   string
		wantErr bool
	}{
		"Daily valid":       {"Daily", false},
		"Weekly valid":      {"Weekly", false},
		"Monthly rejected":  {"Monthly", true},
		"lowercase rejected": {"daily", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags := runStringValidators(ctx, vals, types.StringValue(tc.value))
			require.Equal(t, tc.wantErr, diags.HasError(), "diags: %v", diags)
		})
	}
}

func TestInventorySchema_DestinationFormatValidator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// format lives at destination.bucket.format (two levels of SingleNestedBlock).
	destBlock, ok := inventorySchema(t).Blocks["destination"].(schema.SingleNestedBlock)
	require.True(t, ok, "destination is not a SingleNestedBlock")
	bucketBlock, ok := destBlock.Blocks["bucket"].(schema.SingleNestedBlock)
	require.True(t, ok, "destination.bucket is not a SingleNestedBlock")
	formatAttr, ok := bucketBlock.Attributes["format"].(schema.StringAttribute)
	require.True(t, ok, "destination.bucket.format is not a StringAttribute")
	vals := formatAttr.StringValidators()
	require.NotEmpty(t, vals, "destination.bucket.format should have validators")

	cases := map[string]struct {
		value   string
		wantErr bool
	}{
		"CSV valid":         {"CSV", false},
		"TSV valid":         {"TSV", false},
		"JSON valid":        {"JSON", false},
		"ORC valid":         {"ORC", false},
		"Parquet valid":     {"Parquet", false},
		"Avro rejected":     {"Avro", true},
		"lowercase rejected": {"csv", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags := runStringValidators(ctx, vals, types.StringValue(tc.value))
			require.Equal(t, tc.wantErr, diags.HasError(), "diags: %v", diags)
		})
	}
}
