package testprotos

import "testing"

func TestGeneratedAccessors(t *testing.T) {
	gc := &GlobalContext{UserId: "user", Source: "api", DeviceId: "device"}
	if gc.String() == "" || gc.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("global context reflection failed")
	}
	if gc.GetUserId() != "user" || gc.GetSource() != "api" || gc.GetDeviceId() != "device" {
		t.Fatalf("global context getters failed")
	}
	gc.Reset()
	if gc.GetUserId() != "" {
		t.Fatalf("reset global context should clear fields")
	}
	if (*GlobalContext)(nil).GetUserId() != "" || (*GlobalContext)(nil).GetSource() != "" || (*GlobalContext)(nil).GetDeviceId() != "" {
		t.Fatalf("nil global context getters failed")
	}
	_, _ = (&GlobalContext{}).Descriptor()

	md := &Metadata{CorrelationId: "corr", RequestId: "req", Locale: "en", GlobalContext: &GlobalContext{Source: "api"}}
	if md.String() == "" || md.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("metadata reflection failed")
	}
	if md.GetCorrelationId() != "corr" || md.GetRequestId() != "req" || md.GetLocale() != "en" || md.GetGlobalContext().GetSource() != "api" {
		t.Fatalf("metadata getters failed")
	}
	md.Reset()
	if md.GetCorrelationId() != "" {
		t.Fatalf("reset metadata should clear fields")
	}
	if (*Metadata)(nil).GetCorrelationId() != "" || (*Metadata)(nil).GetRequestId() != "" || (*Metadata)(nil).GetLocale() != "" || (*Metadata)(nil).GetGlobalContext() != nil {
		t.Fatalf("nil metadata getters failed")
	}
	_, _ = (&Metadata{}).Descriptor()

	req := &TestRequest{Metadata: &Metadata{CorrelationId: "corr"}, WorkspaceId: "wrk", ContentType: "image/png", Size: 7, Hash: "sha"}
	if req.String() == "" || req.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("request reflection failed")
	}
	if req.GetMetadata().GetCorrelationId() != "corr" || req.GetWorkspaceId() != "wrk" || req.GetContentType() != "image/png" || req.GetSize() != 7 || req.GetHash() != "sha" {
		t.Fatalf("request getters failed")
	}
	req.Reset()
	if req.GetWorkspaceId() != "" {
		t.Fatalf("reset request should clear fields")
	}
	if (*TestRequest)(nil).GetMetadata() != nil || (*TestRequest)(nil).GetWorkspaceId() != "" || (*TestRequest)(nil).GetContentType() != "" || (*TestRequest)(nil).GetSize() != 0 || (*TestRequest)(nil).GetHash() != "" {
		t.Fatalf("nil request getters failed")
	}
	_, _ = (&TestRequest{}).Descriptor()

	resp := &TestResponse{ResourceId: "res", Status: "ok"}
	if resp.String() == "" || resp.ProtoReflect().Descriptor().FullName() == "" {
		t.Fatalf("response reflection failed")
	}
	if resp.GetResourceId() != "res" || resp.GetStatus() != "ok" {
		t.Fatalf("response getters failed")
	}
	resp.Reset()
	if resp.GetResourceId() != "" {
		t.Fatalf("reset response should clear fields")
	}
	if (*TestResponse)(nil).GetResourceId() != "" || (*TestResponse)(nil).GetStatus() != "" {
		t.Fatalf("nil response getters failed")
	}
	_, _ = (&TestResponse{}).Descriptor()
}
