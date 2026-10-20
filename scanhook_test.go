package sqlx

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// stamp stands in for a type that cannot implement sql.Scanner itself (e.g.
// timestamppb.Timestamp). It has exported fields, so sqlx would otherwise
// treat it as a struct to map columns into.
type stamp struct {
	Sec  int64
	Nano int64
}

// stampScanner scans int64 into **stamp; NULL sets the pointer to nil.
type stampScanner struct {
	p   **stamp
	log *scanLog
}

type scanLog struct {
	mu   sync.Mutex
	vals []interface{}
}

func (l *scanLog) add(v interface{}) {
	l.mu.Lock()
	l.vals = append(l.vals, v)
	l.mu.Unlock()
}

func (l *scanLog) get() []interface{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]interface{}(nil), l.vals...)
}

func (s stampScanner) Scan(src interface{}) error {
	if s.log != nil {
		s.log.add(src)
	}
	switch v := src.(type) {
	case nil:
		*s.p = nil
	case int64:
		*s.p = &stamp{Sec: v}
	default:
		return fmt.Errorf("stampScanner: unsupported %T", src)
	}
	return nil
}

var stampPtrPtr = reflect.TypeOf((**stamp)(nil))

func registerStamp(t *testing.T) *scanLog {
	log := &scanLog{}
	RegisterScanner(stampPtrPtr, func(ptr interface{}) sql.Scanner {
		return stampScanner{p: ptr.(**stamp), log: log}
	})
	t.Cleanup(func() { RegisterScanner(stampPtrPtr, nil) })
	return log
}

// selfScanner implements sql.Scanner itself and must never be wrapped.
type selfScanner struct{ v interface{} }

func (s *selfScanner) Scan(src interface{}) error { s.v = src; return nil }

type stampRec struct {
	ID    int64       `db:"id"`
	TS    *stamp      `db:"ts"`
	Plain *int64      `db:"plain"`
	Self  selfScanner `db:"self"`
}

func sec(s *stamp) interface{} {
	if s == nil {
		return nil
	}
	return s.Sec
}

func TestScanHookStructPaths(t *testing.T) {
	q := "scanhook struct"
	setFakeResult(q, []string{"id", "ts", "plain", "self"},
		[]driver.Value{int64(1), int64(100), int64(5), "a"},
		[]driver.Value{int64(2), nil, int64(6), "b"},
	)
	db := openFake(t)
	defer db.Close()

	check := func(t *testing.T, got []stampRec) {
		t.Helper()
		if len(got) != 2 {
			t.Fatalf("got %d rows", len(got))
		}
		if got[0].ID != 1 || sec(got[0].TS) != int64(100) || *got[0].Plain != 5 || got[0].Self.v != "a" {
			t.Errorf("row 0: %+v ts=%v", got[0], sec(got[0].TS))
		}
		if got[1].ID != 2 || got[1].TS != nil || *got[1].Plain != 6 || got[1].Self.v != "b" {
			t.Errorf("row 1: %+v ts=%v", got[1], sec(got[1].TS))
		}
	}
	wantLog := func(t *testing.T, log *scanLog, want ...interface{}) {
		t.Helper()
		if got := log.get(); !reflect.DeepEqual(got, want) {
			t.Errorf("wrapper received %v, want %v", got, want)
		}
	}

	t.Run("Select", func(t *testing.T) {
		log := registerStamp(t)
		var got []stampRec
		if err := db.Select(&got, q); err != nil {
			t.Fatal(err)
		}
		check(t, got)
		wantLog(t, log, int64(100), nil)
	})
	t.Run("SelectPtrs", func(t *testing.T) {
		log := registerStamp(t)
		var got []*stampRec
		if err := db.Select(&got, q); err != nil {
			t.Fatal(err)
		}
		check(t, []stampRec{*got[0], *got[1]})
		wantLog(t, log, int64(100), nil)
	})
	t.Run("StructScanFunc", func(t *testing.T) {
		log := registerStamp(t)
		rows, err := db.Queryx(q)
		if err != nil {
			t.Fatal(err)
		}
		var got []stampRec
		if err := StructScan(rows, &got); err != nil {
			t.Fatal(err)
		}
		check(t, got)
		wantLog(t, log, int64(100), nil)
	})
	t.Run("RowsStructScan", func(t *testing.T) {
		log := registerStamp(t)
		rows, err := db.Queryx(q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []stampRec
		for rows.Next() {
			var r stampRec
			if err := rows.StructScan(&r); err != nil {
				t.Fatal(err)
			}
			got = append(got, r)
		}
		check(t, got)
		wantLog(t, log, int64(100), nil)
	})
	t.Run("GetAndRowStructScan", func(t *testing.T) {
		log := registerStamp(t)
		var a, b stampRec
		if err := db.Get(&a, q); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowx(q).StructScan(&b); err != nil {
			t.Fatal(err)
		}
		if sec(a.TS) != int64(100) || sec(b.TS) != int64(100) {
			t.Errorf("Get=%v StructScan=%v", sec(a.TS), sec(b.TS))
		}
		wantLog(t, log, int64(100), int64(100))
	})
	t.Run("NamedQueryAndNamedStmt", func(t *testing.T) {
		log := registerStamp(t)
		rows, err := db.NamedQuery(q, map[string]interface{}{})
		if err != nil {
			t.Fatal(err)
		}
		var got []stampRec
		for rows.Next() {
			var r stampRec
			if err := rows.StructScan(&r); err != nil {
				t.Fatal(err)
			}
			got = append(got, r)
		}
		rows.Close()
		check(t, got)

		ns, err := db.PrepareNamed(q)
		if err != nil {
			t.Fatal(err)
		}
		defer ns.Close()
		got = nil
		if err := ns.Select(&got, map[string]interface{}{}); err != nil {
			t.Fatal(err)
		}
		check(t, got)
		wantLog(t, log, int64(100), nil, int64(100), nil)
	})
	t.Run("Unregistered", func(t *testing.T) {
		// Without a registration, **stamp is handed to database/sql
		// unchanged, which cannot convert int64 into it.
		var got []stampRec
		if err := db.Select(&got, q); err == nil {
			t.Fatal("expected scan error without registration")
		}
	})
}

// Registering an unrelated type must leave other fields untouched.
func TestScanHookUnrelatedTypesUntouched(t *testing.T) {
	log := registerStamp(t)
	type rec struct {
		ID    int64       `db:"id"`
		Plain *int64      `db:"plain"`
		Self  selfScanner `db:"self"`
		Val   stamp       `db:"val"`
	}
	q := "scanhook untouched"
	setFakeResult(q, []string{"id", "plain", "self", "val.sec"},
		[]driver.Value{int64(1), nil, "x", int64(9)})
	db := openFake(t)
	defer db.Close()

	var got []rec
	if err := db.Select(&got, q); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 1 || got[0].Plain != nil || got[0].Self.v != "x" || got[0].Val.Sec != 9 {
		t.Errorf("got %+v", got)
	}
	if n := len(log.get()); n != 0 {
		t.Errorf("wrapper called %d times, want 0", n)
	}
}

func TestScanHookNestedAndEmbedded(t *testing.T) {
	log := registerStamp(t)
	type Embedded struct {
		TS *stamp `db:"ts"`
	}
	type inner struct {
		TS2 *stamp `db:"ts2"`
	}
	type rec struct {
		Embedded
		Inner  *inner `db:"inner"`
		Inner2 inner  `db:"inner2"`
	}
	q := "scanhook nested"
	setFakeResult(q, []string{"ts", "inner.ts2", "inner2.ts2"},
		[]driver.Value{int64(1), int64(2), nil})
	db := openFake(t)
	defer db.Close()

	var got rec
	if err := db.Get(&got, q); err != nil {
		t.Fatal(err)
	}
	if sec(got.TS) != int64(1) || got.Inner == nil || sec(got.Inner.TS2) != int64(2) || got.Inner2.TS2 != nil {
		t.Errorf("got ts=%v inner=%+v inner2=%v", sec(got.TS), got.Inner, sec(got.Inner2.TS2))
	}
	if want := []interface{}{int64(1), int64(2), nil}; !reflect.DeepEqual(log.get(), want) {
		t.Errorf("wrapper received %v, want %v", log.get(), want)
	}
}

func TestScanHookNonStruct(t *testing.T) {
	log := registerStamp(t)
	q := "scanhook single"
	setFakeResult(q, []string{"ts"}, []driver.Value{int64(7)})
	qn := "scanhook single null"
	setFakeResult(qn, []string{"ts"}, []driver.Value{nil})
	qs := "scanhook list"
	setFakeResult(qs, []string{"ts"}, []driver.Value{int64(1)}, []driver.Value{nil}, []driver.Value{int64(3)})
	db := openFake(t)
	defer db.Close()

	var ts *stamp
	if err := db.Get(&ts, q); err != nil {
		t.Fatal(err)
	}
	if sec(ts) != int64(7) {
		t.Errorf("Get: %v", sec(ts))
	}
	ts = &stamp{Sec: 99}
	if err := db.Get(&ts, qn); err != nil {
		t.Fatal(err)
	}
	if ts != nil {
		t.Errorf("Get NULL: want nil, got %v", sec(ts))
	}

	var list []*stamp
	if err := db.Select(&list, qs); err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || sec(list[0]) != int64(1) || list[1] != nil || sec(list[2]) != int64(3) {
		t.Errorf("Select: %v", list)
	}

	// StructScan-only paths still reject non-struct destinations.
	if err := db.QueryRowx(q).StructScan(&ts); err == nil {
		t.Error("Row.StructScan into registered non-struct: expected error")
	}
	if want := []interface{}{int64(7), nil, int64(1), nil, int64(3)}; !reflect.DeepEqual(log.get(), want) {
		t.Errorf("wrapper received %v, want %v", log.get(), want)
	}
}

// valScanner scans into *stamp (a value destination).
type valScanner struct{ p *stamp }

func (s valScanner) Scan(src interface{}) error {
	if v, ok := src.(int64); ok {
		s.p.Sec = v
		return nil
	}
	return fmt.Errorf("valScanner: unsupported %T", src)
}

func TestScanHookValueType(t *testing.T) {
	typ := reflect.TypeOf((*stamp)(nil))
	RegisterScanner(typ, func(ptr interface{}) sql.Scanner { return valScanner{ptr.(*stamp)} })
	defer RegisterScanner(typ, nil)

	q := "scanhook value"
	setFakeResult(q, []string{"ts"}, []driver.Value{int64(4)}, []driver.Value{int64(5)})
	db := openFake(t)
	defer db.Close()

	var v stamp
	if err := db.Get(&v, q); err != nil {
		t.Fatal(err)
	}
	if v.Sec != 4 {
		t.Errorf("Get: %+v", v)
	}
	var vs []stamp
	if err := db.Select(&vs, q); err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Sec != 4 || vs[1].Sec != 5 {
		t.Errorf("Select []T: %+v", vs)
	}
	var ps []*stamp
	if err := db.Select(&ps, q); err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Sec != 4 || ps[1].Sec != 5 {
		t.Errorf("Select []*T: %+v", ps)
	}
	type rec struct {
		TS stamp `db:"ts"`
	}
	var rs []rec
	if err := db.Select(&rs, q); err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].TS.Sec != 4 || rs[1].TS.Sec != 5 {
		t.Errorf("Select struct field: %+v", rs)
	}
}

// A wrapper returning nil falls back to scanning the original pointer.
func TestScanHookNilWrapper(t *testing.T) {
	typ := reflect.TypeOf((*int64)(nil))
	var calls int
	var mu sync.Mutex
	RegisterScanner(typ, func(ptr interface{}) sql.Scanner {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	})
	defer RegisterScanner(typ, nil)

	q := "scanhook nilwrap"
	setFakeResult(q, []string{"id"}, []driver.Value{int64(11)})
	db := openFake(t)
	defer db.Close()

	type rec struct {
		ID int64 `db:"id"`
	}
	var r rec
	if err := db.Get(&r, q); err != nil {
		t.Fatal(err)
	}
	var n int64
	if err := db.Get(&n, q); err != nil {
		t.Fatal(err)
	}
	if r.ID != 11 || n != 11 || calls != 2 {
		t.Errorf("r=%+v n=%d calls=%d", r, n, calls)
	}
}

func TestScanHookConcurrent(t *testing.T) {
	registerStamp(t)
	q := "scanhook concurrent"
	setFakeResult(q, []string{"id", "ts", "plain", "self"},
		[]driver.Value{int64(1), int64(100), int64(5), "a"},
		[]driver.Value{int64(2), nil, int64(6), "b"},
	)
	db := openFake(t)
	defer db.Close()

	// Distinct throwaway types registered and removed concurrently with scans.
	others := []reflect.Type{
		reflect.TypeOf((*int8)(nil)), reflect.TypeOf((*uint16)(nil)),
		reflect.TypeOf((*complex64)(nil)), reflect.TypeOf((**float32)(nil)),
	}
	var wg sync.WaitGroup
	for _, typ := range others {
		wg.Add(1)
		go func(typ reflect.Type) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				RegisterScanner(typ, func(ptr interface{}) sql.Scanner { return nil })
				RegisterScanner(typ, nil)
			}
		}(typ)
	}
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				var got []stampRec
				if err := db.Select(&got, q); err != nil {
					errs <- err
					return
				}
				if len(got) != 2 || sec(got[0].TS) != int64(100) || got[1].TS != nil {
					errs <- fmt.Errorf("bad result %+v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
