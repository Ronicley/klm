package main

import "context"

// Reuse the private staging index once a history identity set exceeds its small
// resident allowance; native history length must not multiply live Go maps.
type protocolIDSet struct {
	ctx    context.Context
	dir    string
	memory map[string]bool
	bytes  int
	index  *protocolKeyIndex
}

func newProtocolIDSet(ctx context.Context, dir string) *protocolIDSet {
	return &protocolIDSet{ctx: ctx, dir: dir, memory: map[string]bool{}}
}
func (s *protocolIDSet) Has(id string) (bool, error) {
	if s.index == nil {
		return s.memory[id], nil
	}
	var found bool
	err := s.index.tx.QueryRowContext(s.ctx, `SELECT EXISTS(SELECT 1 FROM object_keys WHERE object_id=1 AND key=?)`, id).Scan(&found)
	return found, err
}
func (s *protocolIDSet) Add(id string) error {
	if s.index == nil && s.bytes+len(id)+64 <= 256<<10 {
		if s.memory[id] {
			return errProtocolJSON
		}
		s.memory[id] = true
		s.bytes += len(id) + 64
		return nil
	}
	if s.index == nil {
		var err error
		s.index, err = newProtocolKeyIndex(s.ctx, s.dir)
		if err != nil {
			return err
		}
		for key := range s.memory {
			if err := s.index.add(1, key); err != nil {
				return err
			}
		}
		s.memory = nil
	}
	return s.index.add(1, id)
}
func (s *protocolIDSet) Close() error {
	if s.index != nil {
		return s.index.Close()
	}
	return nil
}
func protocolIDSeen(set any, id string) (bool, error) {
	switch value := set.(type) {
	case nil:
		return false, nil
	case map[string]bool:
		return value[id], nil
	case *protocolIDSet:
		return value.Has(id)
	}
	return false, errProtocolJSON
}
