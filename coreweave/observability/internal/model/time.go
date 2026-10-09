package model

import (
	"github.com/hashicorp/terraform-plugin-framework-timetypes/timetypes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestampToTimeValue(ts *timestamppb.Timestamp) timetypes.RFC3339 {
	if ts == nil {
		return timetypes.NewRFC3339Null()
	}
	return timetypes.NewRFC3339TimeValue(ts.AsTime())
}
