package objectstorage_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"buf.build/gen/go/coreweave/cwobject/connectrpc/go/cwobject/v1/cwobjectv1connect"
	cwobjectv1 "buf.build/gen/go/coreweave/cwobject/protocolbuffers/go/cwobject/v1"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var (
	errMissingBucketSettings   = errors.New("bucket settings are required")
	errFeatureNotEntitled      = errors.New("organization is not entitled to feature(s): BucketArchive")
	errArchiveDaysRequired     = errors.New("invalid archive settings: archive_after_last_access_days is required when archive is enabled")
	errArchiveDaysBelowMinimum = errors.New("invalid archive settings: archive_after_last_access_days must be >= 60")
	errNotFound                = errors.New("not found")
)

// fakeCWObject is an in-process stand-in for a CWObject service, covering
// the slice of its behavior that coreweave_object_storage_bucket_settings
// depends on.
type fakeCWObject struct {
	cwobjectv1connect.UnimplementedCWObjectHandler

	mu sync.Mutex

	archiveEntitled bool
	archiveMinDays  int32

	// buckets holds the persisted settings, keyed by bucket name.
	buckets map[string]*fakeBucketSettings

	setRequests []*cwobjectv1.CWObjectBucketSettings
}

type fakeBucketSettings struct {
	auditLoggingEnabled        *bool
	archiveEnabled             *bool
	archiveAfterLastAccessDays *int32
	capacityCapBytes           *uint64
}

const fakeArchiveMinDays int32 = 60

// newFakeCWObject starts a fake service holding a single pre-existing bucket
// and returns its base URL. archiveEntitled selects which population the
// caller's organization belongs to.
func newFakeCWObject(t *testing.T, bucketName string, archiveEntitled bool) (string, *fakeCWObject) {
	t.Helper()

	fake := &fakeCWObject{
		archiveEntitled: archiveEntitled,
		archiveMinDays:  fakeArchiveMinDays,
		buckets: map[string]*fakeBucketSettings{
			bucketName: {},
		},
	}

	mux := http.NewServeMux()
	mux.Handle(cwobjectv1connect.NewCWObjectHandler(fake))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv.URL, fake
}

func (f *fakeCWObject) setRequestSettings() []*cwobjectv1.CWObjectBucketSettings {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*cwobjectv1.CWObjectBucketSettings, len(f.setRequests))
	copy(out, f.setRequests)

	return out
}

// storedSettings returns the persisted settings for a bucket, unsanitized, so
// tests can assert on what the service actually kept rather than on what it was
// willing to disclose.
func (f *fakeCWObject) storedSettings(bucketName string) *fakeBucketSettings {
	f.mu.Lock()
	defer f.mu.Unlock()

	stored, ok := f.buckets[bucketName]
	if !ok {
		return nil
	}

	clone := *stored

	return &clone
}

func hasArchiveFields(settings *cwobjectv1.CWObjectBucketSettings) bool {
	if settings == nil {
		return false
	}

	return settings.GetArchiveEnabled() != nil || settings.GetArchiveAfterLastAccessDays() != nil
}

func (f *fakeCWObject) sanitizeForOrg(settings *cwobjectv1.CWObjectBucketSettings) *cwobjectv1.CWObjectBucketSettings {
	if settings == nil || f.archiveEntitled {
		return settings
	}

	out := &cwobjectv1.CWObjectBucketSettings{
		AuditLoggingEnabled: settings.GetAuditLoggingEnabled(),
	}

	return out
}

func (b *fakeBucketSettings) toProto() *cwobjectv1.CWObjectBucketSettings {
	out := &cwobjectv1.CWObjectBucketSettings{}

	if b.auditLoggingEnabled != nil {
		out.AuditLoggingEnabled = wrapperspb.Bool(*b.auditLoggingEnabled)
	}

	if b.archiveEnabled != nil {
		out.ArchiveEnabled = wrapperspb.Bool(*b.archiveEnabled)
	}

	if b.archiveAfterLastAccessDays != nil {
		out.ArchiveAfterLastAccessDays = wrapperspb.Int32(*b.archiveAfterLastAccessDays)
	}

	// Surface the cap through the read-only field.
	if b.capacityCapBytes != nil {
		out.SetConfiguredCapacityCapBytes(wrapperspb.UInt64(*b.capacityCapBytes))
	}

	return out
}

func (f *fakeCWObject) SetBucketSettings(
	_ context.Context, req *connect.Request[cwobjectv1.SetBucketSettingsRequest],
) (*connect.Response[cwobjectv1.SetBucketSettingsResponse], error) {
	settings := req.Msg.GetSettings()

	if settings == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errMissingBucketSettings)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.setRequests = append(f.setRequests, settings)

	if hasArchiveFields(settings) && !f.archiveEntitled {
		return nil, connect.NewError(connect.CodePermissionDenied, errFeatureNotEntitled)
	}

	if hasArchiveFields(settings) {
		if err := f.validateArchiveSettings(settings); err != nil {
			return nil, err
		}
	}

	stored, ok := f.buckets[req.Msg.GetBucketName()]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errNotFound)
	}

	f.persist(stored, settings)

	return connect.NewResponse(&cwobjectv1.SetBucketSettingsResponse{
		Settings: f.sanitizeForOrg(stored.toProto()),
	}), nil
}

func (f *fakeCWObject) validateArchiveSettings(settings *cwobjectv1.CWObjectBucketSettings) error {
	enabled := settings.GetArchiveEnabled()
	days := settings.GetArchiveAfterLastAccessDays()

	if enabled.GetValue() && days == nil {
		return connect.NewError(connect.CodeInvalidArgument, errArchiveDaysRequired)
	}

	if days != nil && days.GetValue() < f.archiveMinDays {
		return connect.NewError(connect.CodeInvalidArgument, errArchiveDaysBelowMinimum)
	}

	return nil
}

func (f *fakeCWObject) persist(stored *fakeBucketSettings, settings *cwobjectv1.CWObjectBucketSettings) {
	if audit := settings.GetAuditLoggingEnabled(); audit != nil {
		value := audit.GetValue()
		stored.auditLoggingEnabled = &value
	}

	// Persist a set cap (0 valid); before the archive switch, which returns early.
	if settings.HasCapacityCapBytes() {
		value := settings.GetCapacityCapBytes()
		stored.capacityCapBytes = &value
	}

	enabled := settings.GetArchiveEnabled()
	days := settings.GetArchiveAfterLastAccessDays()

	switch {
	case enabled == nil && days == nil:
		return

	case enabled != nil && !enabled.GetValue():
		value := false
		stored.archiveEnabled = &value
		stored.archiveAfterLastAccessDays = nil

	case enabled != nil && enabled.GetValue():
		value := true
		stored.archiveEnabled = &value

		if days != nil {
			retention := days.GetValue()
			stored.archiveAfterLastAccessDays = &retention
		}

	case days != nil:
		retention := days.GetValue()
		stored.archiveAfterLastAccessDays = &retention
	}
}

func (f *fakeCWObject) GetBucketInfo(
	_ context.Context, req *connect.Request[cwobjectv1.GetBucketInfoRequest],
) (*connect.Response[cwobjectv1.GetBucketInfoResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stored, ok := f.buckets[req.Msg.GetBucketName()]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errNotFound)
	}

	return connect.NewResponse(&cwobjectv1.GetBucketInfoResponse{
		Info: &cwobjectv1.BucketInfo{
			Name:     req.Msg.GetBucketName(),
			Settings: f.sanitizeForOrg(stored.toProto()),
		},
	}), nil
}
