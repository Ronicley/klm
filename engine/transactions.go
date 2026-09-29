package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Format 1 patches address exported JSON fields, slice positions and map keys.
// A frame contains the ENTIRE transaction (including queue payloads/receipts and
// graph/consultation transitions); there is no independently committed row.
type statePatch struct {
	Path   []string        `json:"path"`
	Value  json.RawMessage `json:"value,omitempty"`
	Length *int            `json:"length,omitempty"`
	Delete bool            `json:"delete,omitempty"`
}

type stateTransaction struct {
	events     map[string]map[int]Event
	indices    map[string]map[string]int
	baseEvents map[string]int
}

var sessionType = reflect.TypeOf(Session{})
var eventSliceType = reflect.TypeOf([]Event{})

// Strings are immutable and shared, not encoded/copied. Histories are shared
// append-only; replacements MUST use setEvent. All other mutable metadata is
// private to the callback, including nested queue, graph and permission data.
func cloneTransactionValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Struct:
		n := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			if !n.Field(i).CanSet() || v.Type().Field(i).PkgPath != "" {
				continue
			}
			if v.Type() == sessionType && v.Type().Field(i).Name == "Events" {
				n.Field(i).Set(v.Field(i))
			} else {
				n.Field(i).Set(cloneTransactionValue(v.Field(i)))
			}
		}
		return n
	case reflect.Slice:
		// Activation event logs are append-only too; do not clone their history.
		if v.Type() == eventSliceType {
			return v
		}
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		n := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			n.Index(i).Set(cloneTransactionValue(v.Index(i)))
		}
		return n
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		n := reflect.MakeMapWithSize(v.Type(), v.Len())
		it := v.MapRange()
		for it.Next() {
			n.SetMapIndex(it.Key(), cloneTransactionValue(it.Value()))
		}
		return n
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		n := reflect.New(v.Type().Elem())
		n.Elem().Set(cloneTransactionValue(v.Elem()))
		return n
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		n := reflect.New(v.Type()).Elem()
		n.Set(cloneTransactionValue(v.Elem()))
		return n
	default:
		return v
	}
}

func transactionState(d diskState) diskState {
	n := cloneTransactionValue(reflect.ValueOf(d)).Interface().(diskState)
	n.tx = &stateTransaction{events: map[string]map[int]Event{}, baseEvents: map[string]int{}}
	for _, s := range d.Sessions {
		n.tx.baseEvents[s.ID] = len(s.Events)
	}
	return n
}

func (d *diskState) setEvent(s *Session, index int, e Event) {
	if d.tx == nil {
		s.Events[index] = e
		return
	}
	if d.tx.events[s.ID] == nil {
		d.tx.events[s.ID] = map[int]Event{}
	}
	d.tx.events[s.ID][index] = e
}

func patchPath(path []string, field string) []string {
	n := make([]string, len(path)+1)
	copy(n, path)
	n[len(path)] = field
	return n
}

func diffTransaction(before, after reflect.Value, path []string, patches *[]statePatch) error {
	put := func() error {
		b, err := json.Marshal(after.Interface())
		if err == nil {
			*patches = append(*patches, statePatch{Path: path, Value: b})
		}
		return err
	}
	switch after.Kind() {
	case reflect.Struct:
		for i := 0; i < after.NumField(); i++ {
			field := after.Type().Field(i)
			if field.PkgPath != "" || after.Type() == sessionType && field.Name == "Events" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			if err := diffTransaction(before.Field(i), after.Field(i), patchPath(path, name), patches); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if after.IsNil() {
			if !before.IsNil() {
				return put()
			}
			return nil
		}
		if before.IsNil() || before.Len() != after.Len() {
			n := after.Len()
			*patches = append(*patches, statePatch{Path: path, Length: &n})
		}
		start := 0
		if after.Type() == eventSliceType {
			if after.Len() < before.Len() {
				return errors.New("activation event history is append-only")
			}
			start = before.Len()
		}
		for i := start; i < after.Len(); i++ {
			old := reflect.Zero(after.Type().Elem())
			if i < before.Len() {
				old = before.Index(i)
			}
			if err := diffTransaction(old, after.Index(i), patchPath(path, strconv.Itoa(i)), patches); err != nil {
				return err
			}
		}
	case reflect.Map:
		if after.IsNil() {
			if !before.IsNil() {
				return put()
			}
			return nil
		}
		if before.IsNil() {
			*patches = append(*patches, statePatch{Path: path, Value: json.RawMessage(`{}`)})
		}
		it := before.MapRange()
		for it.Next() {
			if !after.MapIndex(it.Key()).IsValid() {
				*patches = append(*patches, statePatch{Path: patchPath(path, it.Key().String()), Delete: true})
			}
		}
		it = after.MapRange()
		for it.Next() {
			old := before.MapIndex(it.Key())
			if old.IsValid() && reflect.DeepEqual(old.Interface(), it.Value().Interface()) {
				continue
			}
			b, err := json.Marshal(it.Value().Interface())
			if err != nil {
				return err
			}
			*patches = append(*patches, statePatch{Path: patchPath(path, it.Key().String()), Value: b})
		}
	default:
		if !reflect.DeepEqual(before.Interface(), after.Interface()) {
			return put()
		}
	}
	return nil
}

func applyStatePatch(v reflect.Value, p statePatch) error {
	if len(p.Path) == 0 {
		if p.Delete {
			return errors.New("delete requires map key")
		}
		if p.Length != nil {
			if v.Kind() != reflect.Slice || *p.Length < 0 {
				return errors.New("invalid transaction resize")
			}
			if *p.Length <= v.Cap() && !v.IsNil() {
				oldLen := v.Len()
				v.SetLen(*p.Length)
				for i := oldLen; i < v.Len(); i++ {
					v.Index(i).SetZero()
				}
			} else {
				n := reflect.MakeSlice(v.Type(), *p.Length, max(*p.Length, v.Cap()+v.Cap()/2))
				reflect.Copy(n, v)
				v.Set(n)
			}
			return nil
		}
		// Replacement, not JSON merge: omitted fields must clear previous values.
		n := reflect.New(v.Type())
		if err := json.Unmarshal(p.Value, n.Interface()); err != nil {
			return err
		}
		v.Set(n.Elem())
		return nil
	}
	key := p.Path[0]
	p.Path = p.Path[1:]
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.PkgPath != "" {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" {
				name = f.Name
			}
			if name == key {
				return applyStatePatch(v.Field(i), p)
			}
		}
	case reflect.Slice:
		i, err := strconv.Atoi(key)
		if err == nil && i >= 0 && i < v.Len() {
			return applyStatePatch(v.Index(i), p)
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String || len(p.Path) != 0 {
			break
		}
		k := reflect.ValueOf(key).Convert(v.Type().Key())
		if p.Delete {
			v.SetMapIndex(k, reflect.Value{})
			return nil
		}
		n := reflect.New(v.Type().Elem()).Elem()
		if err := applyStatePatch(n, p); err != nil {
			return err
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		v.SetMapIndex(k, n)
		return nil
	}
	return fmt.Errorf("invalid transaction path component %q", key)
}

func replayTransaction(state *diskState, patches []statePatch) error {
	// Session ordering is UI metadata. Preserve histories by identity when the
	// session slice is reordered; never serialize those histories for a move.
	histories := make(map[string][]Event, len(state.Sessions))
	for _, s := range state.Sessions {
		histories[s.ID] = s.Events
	}
	isHistory := func(p statePatch) bool { return len(p.Path) >= 3 && p.Path[0] == "sessions" && p.Path[2] == "events" }
	for _, p := range patches {
		if !isHistory(p) {
			if err := applyStatePatch(reflect.ValueOf(state).Elem(), p); err != nil {
				return err
			}
		}
	}
	for i := range state.Sessions {
		state.Sessions[i].Events = histories[state.Sessions[i].ID]
	}
	for _, p := range patches {
		if isHistory(p) {
			if err := applyStatePatch(reflect.ValueOf(state).Elem(), p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *app) commitTransactionLocked(change func(*diskState) error) error {
	if a.storageErr != nil {
		return a.storageErr
	}
	a.indexMessageIDsLocked()
	next := transactionState(a.state)
	next.tx.indices = a.streamIndexes
	if err := change(&next); err != nil {
		return err
	}
	normalizeGraphWorkspaceRecords(&next)
	normalizeTranscriptionSettings(&next.Transcription)
	if a.graphValidation == nil {
		a.graphValidation = map[string]validatedGraphSnapshot{}
	}
	if err := validateGraphRecordsCached(&next, a.graphValidation); err != nil {
		return err
	}
	var patches []statePatch
	if err := diffTransaction(reflect.ValueOf(a.state), reflect.ValueOf(next), nil, &patches); err != nil {
		return a.failStorageLocked(err)
	}
	changes := map[string][]EventChange{}
	revision := a.state.GraphRevision + 1
	for i := range next.Sessions {
		s := &next.Sessions[i]
		old := a.state.session(s.ID)
		start := 0
		if old != nil {
			start = len(old.Events)
		}
		if len(s.Events) < start {
			return a.failStorageLocked(errors.New("transaction cannot truncate session history"))
		}
		path := []string{"sessions", strconv.Itoa(i), "events"}
		if old == nil || len(s.Events) != start {
			n := len(s.Events)
			patches = append(patches, statePatch{Path: path, Length: &n})
		}
		edits := next.tx.events[s.ID]
		for j := start; j < len(s.Events); j++ {
			if edits == nil {
				edits = map[int]Event{}
			}
			if _, ok := edits[j]; !ok {
				edits[j] = s.Events[j]
			}
		}
		for index, e := range edits {
			if old != nil && index < start && reflect.DeepEqual(old.Events[index], e) {
				continue
			}
			b, err := json.Marshal(e)
			if err != nil {
				return a.failStorageLocked(err)
			}
			patches = append(patches, statePatch{Path: patchPath(path, strconv.Itoa(index)), Value: b})
			changes[s.ID] = append(changes[s.ID], EventChange{Index: index, Revision: revision, Event: e})
		}
	}
	if len(patches) == 0 {
		return nil
	}
	if err := appendStreamRecord(a.dir, streamRecord{Revision: revision, Format: 1, Transaction: patches}); err != nil {
		return a.failStorageLocked(err)
	}
	// Only now can replacements touch the shared history arrays. HTTP history
	// projections own their slice window; Event payloads themselves are immutable.
	for id, edits := range changes {
		s := next.session(id)
		for _, edit := range edits {
			s.Events[edit.Index] = edit.Event
		}
	}
	next.tx = nil
	next.GraphRevision, next.GraphViewRevision = revision, revision
	a.state = next
	for id, edits := range changes {
		sort.Slice(edits, func(i, j int) bool { return edits[i].Index < edits[j].Index })
		a.appendHistoryChangesLocked(id, revision, edits)
		if index := a.streamIndexes[id]; index != nil {
			for _, edit := range edits {
				index[edit.Event.ID] = edit.Index
			}
		}
		if a.messageIDs != nil {
			for _, edit := range edits {
				a.messageIDs[edit.Event.ID] = true
			}
		}
	}
	a.notifyAllLocked()
	return nil
}
