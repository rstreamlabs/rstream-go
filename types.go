// See LICENSE file in the project root for license information.

package rstream

import (
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func StringPtr(s string) *string { return &s }
func BoolPtr(b bool) *bool       { return &b }
func Uint16Ptr(u uint16) *uint16 { return &u }

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
