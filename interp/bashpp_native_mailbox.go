package interp

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"
)

// The callback mailbox is a bounded fast path for the small, synchronous
// original-method callbacks which otherwise require two socket wakeups each.
// The authenticated control connection remains the source of session lifetime
// and the fallback for messages which do not fit. Each slot has one producer
// and one consumer at a time; release/acquire atomics publish the payload.
const (
	bashPPMailboxSlots    = 16
	bashPPMailboxSlotSize = 64 << 10
	bashPPMailboxHeader   = 64
	bashPPMailboxHalf     = (bashPPMailboxSlotSize - bashPPMailboxHeader) / 2
	bashPPMailboxSize     = bashPPMailboxSlots * bashPPMailboxSlotSize
	bashPPMailboxFree     = 0
	bashPPMailboxWriting  = 1
	bashPPMailboxRequest  = 2
	bashPPMailboxServing  = 3
	bashPPMailboxReply    = 4
)

type bashPPCallbackMailbox struct {
	data    []byte
	path    string
	cleanup func() error
	mu      sync.Mutex
	refs    int
	closing bool
	once    sync.Once
}

func (m *bashPPCallbackMailbox) word(slot, offset int) *uint32 {
	return (*uint32)(unsafe.Pointer(&m.data[slot*bashPPMailboxSlotSize+offset]))
}

func (m *bashPPCallbackMailbox) requestBytes(slot int) []byte {
	start := slot*bashPPMailboxSlotSize + bashPPMailboxHeader
	return m.data[start : start+bashPPMailboxHalf]
}

func (m *bashPPCallbackMailbox) replyBytes(slot int) []byte {
	start := slot*bashPPMailboxSlotSize + bashPPMailboxHeader + bashPPMailboxHalf
	return m.data[start : (slot+1)*bashPPMailboxSlotSize]
}

// take claims the next published callback. Multiple nested request goroutines
// may inspect the map, but the active-callback check in request ensures only
// the interpreter frame which currently owns callback execution calls take.
func (m *bashPPCallbackMailbox) take() (int, bashPPBridgeResponse, bool) {
	if m == nil {
		return 0, bashPPBridgeResponse{}, false
	}
	for slot := 0; slot < bashPPMailboxSlots; slot++ {
		state := m.word(slot, 0)
		if !atomic.CompareAndSwapUint32(state, bashPPMailboxRequest, bashPPMailboxServing) {
			continue
		}
		n := int(atomic.LoadUint32(m.word(slot, 4)))
		var q bashPPBridgeResponse
		if n <= 0 || n > len(m.requestBytes(slot)) {
			q.Error = "gosource: invalid callback mailbox request size"
		} else if err := json.Unmarshal(m.requestBytes(slot)[:n], &q); err != nil {
			q.Error = fmt.Sprintf("gosource: invalid callback mailbox request: %v", err)
		}
		return slot, q, true
	}
	return 0, bashPPBridgeResponse{}, false
}

// answer publishes a reply only when it fits. The caller sends larger replies
// over the authenticated control connection, then publishes a small marker.
func (m *bashPPCallbackMailbox) answer(slot int, answer bashPPBridgeRequest) bool {
	payload, err := json.Marshal(answer)
	if err != nil || len(payload) > len(m.replyBytes(slot)) {
		return false
	}
	copy(m.replyBytes(slot), payload)
	atomic.StoreUint32(m.word(slot, 8), uint32(len(payload)))
	atomic.StoreUint32(m.word(slot, 0), bashPPMailboxReply)
	return true
}

func (m *bashPPCallbackMailbox) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.closing = true
	ready := m.refs == 0
	m.mu.Unlock()
	if ready {
		m.once.Do(func() { _ = m.cleanup() })
	}
}

func (m *bashPPCallbackMailbox) retain() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return false
	}
	m.refs++
	return true
}

func (m *bashPPCallbackMailbox) release() {
	m.mu.Lock()
	m.refs--
	ready := m.closing && m.refs == 0
	m.mu.Unlock()
	if ready {
		m.once.Do(func() { _ = m.cleanup() })
	}
}

func bashPPMailboxWorkerSource(enabled bool) (imports, implementation string) {
	if !enabled {
		return "", `func openCallbackMailbox(){}
func mailboxCallback(response)(request,bool,error){return request{},false,nil}`
	}
	imports = `
 "os"
 "sync/atomic"
 "syscall"
`
	implementation = fmt.Sprintf(`
const callbackMailboxSlots=%d
const callbackMailboxSlotSize=%d
const callbackMailboxHeader=%d
const callbackMailboxHalf=%d
const callbackMailboxFree uint32=%d
const callbackMailboxWriting uint32=%d
const callbackMailboxRequest uint32=%d
const callbackMailboxReply uint32=%d
var callbackMailbox []byte
func callbackMailboxWord(slot,offset int)*uint32{return (*uint32)(unsafe.Pointer(&callbackMailbox[slot*callbackMailboxSlotSize+offset]))}
func callbackMailboxRequestBytes(slot int)[]byte{start:=slot*callbackMailboxSlotSize+callbackMailboxHeader;return callbackMailbox[start:start+callbackMailboxHalf]}
func callbackMailboxReplyBytes(slot int)[]byte{start:=slot*callbackMailboxSlotSize+callbackMailboxHeader+callbackMailboxHalf;return callbackMailbox[start:(slot+1)*callbackMailboxSlotSize]}
%s
func mailboxCallback(q response)(request,bool,error){
 if callbackMailbox==nil{return request{},false,nil}
 payload,err:=json.Marshal(q);if err!=nil||len(payload)>callbackMailboxHalf{return request{},false,err}
 slot:=-1
 for i:=0;i<callbackMailboxSlots;i++{if atomic.CompareAndSwapUint32(callbackMailboxWord(i,0),callbackMailboxFree,callbackMailboxWriting){slot=i;break}}
 if slot<0{return request{},false,nil}
 copy(callbackMailboxRequestBytes(slot),payload);atomic.StoreUint32(callbackMailboxWord(slot,4),uint32(len(payload)));atomic.StoreUint32(callbackMailboxWord(slot,0),callbackMailboxRequest)
 for atomic.LoadUint32(callbackMailboxWord(slot,0))!=callbackMailboxReply{select{case <-handles.stop:atomic.StoreUint32(callbackMailboxWord(slot,0),callbackMailboxFree);return request{},true,fmt.Errorf("interpreter connection closed during callback");default:}}
 n:=int(atomic.LoadUint32(callbackMailboxWord(slot,8)));var reply request
 if n<=0||n>len(callbackMailboxReplyBytes(slot)){err=fmt.Errorf("invalid callback mailbox reply size")}else{err=json.Unmarshal(callbackMailboxReplyBytes(slot)[:n],&reply)}
 atomic.StoreUint32(callbackMailboxWord(slot,0),callbackMailboxFree)
 return reply,true,err
}
`, bashPPMailboxSlots, bashPPMailboxSlotSize, bashPPMailboxHeader, bashPPMailboxHalf, bashPPMailboxFree, bashPPMailboxWriting, bashPPMailboxRequest, bashPPMailboxReply, bashPPMailboxOpenWorkerSource())
	return imports, implementation
}

func bashPPMailboxYield() {}
