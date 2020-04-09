// MIT License

// Copyright (c) 2020 Tree Xie

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package elton

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

type (
	// SignedKeysGenerator signed keys generator
	SignedKeysGenerator interface {
		GetKeys() []string
		SetKeys([]string)
	}
	// SimpleSignedKeys simple sigined key
	SimpleSignedKeys struct {
		keys []string
	}
	// RWMutexSignedKeys read/write mutex signed key
	RWMutexSignedKeys struct {
		sync.RWMutex
		keys []string
	}
	// AtomicSignedKeys atomic toggle signed keys
	AtomicSignedKeys struct {
		keys *[]string
	}
)

// GetKeys get keys
func (sk *SimpleSignedKeys) GetKeys() []string {
	return sk.keys
}

// SetKeys set keys
func (sk *SimpleSignedKeys) SetKeys(keys []string) {
	sk.keys = keys
}

// GetKeys get keys
func (rwSk *RWMutexSignedKeys) GetKeys() []string {
	rwSk.RLock()
	defer rwSk.RUnlock()
	return rwSk.keys
}

// SetKeys set keys
func (rwSk *RWMutexSignedKeys) SetKeys(keys []string) {
	rwSk.Lock()
	defer rwSk.Unlock()
	rwSk.keys = keys
}

// GetKeys get keys
func (atSk *AtomicSignedKeys) GetKeys() []string {
	keysPoint := (*[]string)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&atSk.keys))))
	return *keysPoint
}

// SetKeys set keys
func (atSk *AtomicSignedKeys) SetKeys(keys []string) {
	s := keys[0:]
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&atSk.keys)), unsafe.Pointer(&s))
}
