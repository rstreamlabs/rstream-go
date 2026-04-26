// See LICENSE file in the project root for license information.

package rstream

import (
	"net"
	"time"

	"github.com/rstreamlabs/rstream-go/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func StringPtr(s string) *string { return &s }
func BoolPtr(b bool) *bool       { return &b }
func Uint16Ptr(u uint16) *uint16 { return &u }
func Uint32Ptr(u uint32) *uint32 { return &u }

func StrOrUndef(s *string) string {
	if s == nil {
		return "undefined"
	}
	return *s
}

func TunnelTypePtr(t TunnelType) *TunnelType    { return &t }
func ProtocolPtr(p Protocol) *Protocol          { return &p }
func TLSModePtr(t TLSMode) *TLSMode             { return &t }
func HTTPVersionPtr(h HTTPVersion) *HTTPVersion { return &h }

func stringPbValueOrNil(s *string) *wrapperspb.StringValue {
	if s == nil {
		return nil
	}
	return &wrapperspb.StringValue{Value: *s}
}

func boolPbValueOrNil(b *bool) *wrapperspb.BoolValue {
	if b == nil {
		return nil
	}
	return &wrapperspb.BoolValue{Value: *b}
}

func uint32PbValueOrNil(u *uint32) *wrapperspb.UInt32Value {
	if u == nil {
		return nil
	}
	return &wrapperspb.UInt32Value{Value: *u}
}

func timestampPbValueOrNil(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func stringPtrFromPbValue(s *wrapperspb.StringValue) *string {
	if s == nil {
		return nil
	}
	return &s.Value
}

func boolPtrFromPbValue(b *wrapperspb.BoolValue) *bool {
	if b == nil {
		return nil
	}
	return &b.Value
}

func uint32PtrFromPbValue(u *wrapperspb.UInt32Value) *uint32 {
	if u == nil {
		return nil
	}
	return &u.Value
}

func timePtrFromPbValue(t *timestamppb.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	ti := t.AsTime()
	return &ti
}

func Uint32ToIPv4(ip uint32) net.IP {
	return net.IPv4(
		byte(ip>>24),
		byte(ip>>16),
		byte(ip>>8),
		byte(ip),
	)
}

func NetIPFromPbValue(ip *pb.IpAddress) net.IP {
	if ip == nil {
		return nil
	}
	if x, ok := ip.Addr.(*pb.IpAddress_V4); ok {
		return Uint32ToIPv4(x.V4)
	}
	if x, ok := ip.Addr.(*pb.IpAddress_V6); ok {
		return net.IP(x.V6)
	}
	return nil
}
