package sqlx

import (
	"database/sql"
	"reflect"
	"sync"
	"sync/atomic"
)

// ScanWrapper converts a scan destination pointer into a sql.Scanner that
// writes the scanned value through that pointer.
type ScanWrapper func(ptr interface{}) sql.Scanner

var (
	scanRegistryMu sync.Mutex
	// scanRegistry holds a map[reflect.Type]ScanWrapper that is replaced, never
	// mutated, on registration (copy-on-write), so readers need no lock.
	scanRegistry atomic.Value
)

// RegisterScanner makes sqlx wrap scan destinations whose type is t (the
// pointer-to-field type, e.g. **timestamp.Timestamp for a field of type
// *timestamp.Timestamp) with wrap(ptr), and pass the returned sql.Scanner to
// database/sql instead of ptr. Passing a nil wrap removes the registration.
// If wrap returns nil for a given pointer, the original pointer is scanned.
//
// SAFEGUARD FORK: this hook exists only in github.com/SafeguardProperties/sqlx
// (branch safeguard-scan-hook, based on upstream v1.3.5). Conduit uses it to
// scan protobuf Timestamps after the move to protobuf APIv2, where the
// Timestamp type can no longer implement sql.Scanner itself (conduit ADR 0002).
//
// The hook applies wherever sqlx builds scan destinations:
//   - struct fields, including nested and embedded ones (StructScan, Get,
//     Select, Rows.StructScan, Row.StructScan, NamedQuery/NamedStmt results);
//   - non-struct destinations of Get and Select, e.g. db.Get(&ts, ...) with
//     ts of type *T and **T registered, or db.Select(&[]*T{}, ...) for which
//     each element is scanned through a wrapped **T (NULL yields a nil
//     element if the wrapper sets it).
//
// It does not apply to raw Rows.Scan / Row.Scan calls, MapScan or SliceScan.
//
// RegisterScanner is safe for concurrent use, but is intended to be called
// during initialization. Registered types are looked up once per query (per
// column set), not per row; when nothing is registered the cost is a single
// atomic load per query.
func RegisterScanner(t reflect.Type, wrap func(ptr interface{}) sql.Scanner) {
	scanRegistryMu.Lock()
	defer scanRegistryMu.Unlock()

	old, _ := scanRegistry.Load().(map[reflect.Type]ScanWrapper)
	next := make(map[reflect.Type]ScanWrapper, len(old)+1)
	for k, v := range old {
		next[k] = v
	}
	if wrap == nil {
		delete(next, t)
	} else {
		next[t] = wrap
	}
	scanRegistry.Store(next)
}

// scanRegistrations returns the current registry, or nil if empty.
func scanRegistrations() map[reflect.Type]ScanWrapper {
	m, _ := scanRegistry.Load().(map[reflect.Type]ScanWrapper)
	if len(m) == 0 {
		return nil
	}
	return m
}

// scanWrapperFor returns the wrapper registered for type t, or nil.
func scanWrapperFor(t reflect.Type) ScanWrapper {
	m := scanRegistrations()
	if m == nil {
		return nil
	}
	return m[t]
}

// scanWrappersFor resolves, once per column set, the wrapper for each
// traversal into struct type base. It returns nil if no field is registered.
func scanWrappersFor(base reflect.Type, traversals [][]int) []ScanWrapper {
	m := scanRegistrations()
	if m == nil {
		return nil
	}
	base = deref(base)
	var wraps []ScanWrapper
	for i, traversal := range traversals {
		if len(traversal) == 0 {
			continue
		}
		ft := fieldTypeByIndexes(base, traversal)
		if w := m[reflect.PtrTo(ft)]; w != nil {
			if wraps == nil {
				wraps = make([]ScanWrapper, len(traversals))
			}
			wraps[i] = w
		}
	}
	return wraps
}

// fieldTypeByIndexes mirrors reflectx.FieldByIndexes on types: it walks the
// traversal, dereferencing pointer-to-struct fields along the way.
func fieldTypeByIndexes(t reflect.Type, indexes []int) reflect.Type {
	for _, i := range indexes {
		t = deref(t).Field(i).Type
	}
	return t
}

func deref(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Ptr {
		return t.Elem()
	}
	return t
}

// applyScanWrappers replaces values[i] with wraps[i](values[i]) where set.
func applyScanWrappers(values []interface{}, wraps []ScanWrapper) {
	for i, w := range wraps {
		if w == nil {
			continue
		}
		if s := w(values[i]); s != nil {
			values[i] = s
		}
	}
}

// wrapScanDest wraps a non-struct scan destination if its type is registered.
func wrapScanDest(dest interface{}, t reflect.Type) interface{} {
	if w := scanWrapperFor(t); w != nil {
		if s := w(dest); s != nil {
			return s
		}
	}
	return dest
}
