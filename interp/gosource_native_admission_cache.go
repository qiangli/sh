package interp

// Sprint: #243; Story: #676; Story-ID: 8aaa61718184

import (
	"errors"
	"mvdan.cc/sh/v3/syntax"
)

// Destination is the scoped bridge type identity under the session's immutable
// import/local-type registry. Source is a worker-issued reflect.Type token,
// authenticated against a returned handle; neither display strings nor caller
// supplied NativeTypeID fields establish source identity.
type goSourceNativeAdmissionKey struct {
	source      uint64
	destination string
}

func (s *bashPPNativeSession) rememberNativeHandleType(v bashPPBridgeValue) {
	if v.Kind != "handle" || v.Handle == 0 || v.NativeTypeID == 0 || v.Session != s.id {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handleTypes == nil {
		s.handleTypes = make(map[uint64]uint64)
	}
	s.handleTypes[v.Handle] = v.NativeTypeID
}

func (s *bashPPNativeSession) nativeAdmissionKey(value bashPPBridgeValue, destination string) (goSourceNativeAdmissionKey, bool) {
	if value.Kind != "handle" || value.Session != s.id || destination == "" {
		return goSourceNativeAdmissionKey{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.handleTypes[value.Handle]
	if id == 0 {
		return goSourceNativeAdmissionKey{}, false
	}
	return goSourceNativeAdmissionKey{source: id, destination: destination}, true
}

func (s *bashPPNativeSession) rememberNativeAdmission(key goSourceNativeAdmissionKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interfaceAdmissions == nil {
		s.interfaceAdmissions = make(map[goSourceNativeAdmissionKey]bool)
	}
	s.interfaceAdmissions[key] = true
}

// begin keeps the existing registry-change refusal before any cache hit. Reset
// and bridge replacement allocate a new session and therefore an empty cache.
// Cancellation and closed sessions never become successful cached operations.
func (r *Runner) goSourceNativeAdmission(value bashPPBridgeValue, iface *syntax.BashPPInterfaceType) (*bashPPNativeSession, goSourceNativeAdmissionKey, bool, error) {
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return nil, goSourceNativeAdmissionKey{}, false, err
	}
	s := req.Bridge
	if s == nil {
		return nil, goSourceNativeAdmissionKey{}, false, nil
	}
	if err := r.ectx.Err(); err != nil {
		return nil, goSourceNativeAdmissionKey{}, false, err
	}
	if err := s.begin(r.ectx, req); err != nil {
		return nil, goSourceNativeAdmissionKey{}, false, err
	}
	select {
	case <-s.done:
		return nil, goSourceNativeAdmissionKey{}, false, errors.New("gosource: native dependency session closed")
	default:
	}
	key, ok := s.nativeAdmissionKey(value, r.bashPPBridgeTypeIdentity(iface))
	if !ok {
		return nil, key, false, nil
	}
	s.mu.Lock()
	cached := s.interfaceAdmissions[key]
	s.mu.Unlock()
	return s, key, cached, nil
}
