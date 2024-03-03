package session

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/nsf/jsondiff"
)

type KVState struct {
	Revision uint64          `json:"revision"`
	Value    json.RawMessage `json:"value"`
}
type KVSetIf struct {
	Revision uint64          `json:"revision"`
	Value    json.RawMessage `json:"value"`
}
type KVCompareAndSwap struct {
	Expected json.RawMessage `json:"expected"`
	Desired  json.RawMessage `json:"desired"`
}
type KVCompareAndSwapResult struct {
	OK       bool            `json:"ok"`
	Diff     json.RawMessage `json:"diff"`
	Revision uint64          `json:"revision"`
	Value    json.RawMessage `json:"value"`
}

func getKeyState(ctx context.Context, session *Session, key string) (state KVState, err error) {
	kv := session.Nats.DefaultKVStore(key)
	v, e := kv.Get(ctx, key)
	if e == jetstream.ErrKeyNotFound {
		kv.Create(ctx, key, []byte("null"))
		v, e = kv.Get(ctx, key)
	}
	if e != nil {
		err = e
		return
	}
	state.Revision = v.Revision()
	state.Value = v.Value()
	return
}

func init() {
	Match("/kv/{key}/cas", func(session *Session, r *http.Request, p KVCompareAndSwap) (res KVCompareAndSwapResult, err error) {
		key := r.PathValue("key")
		kv := session.Nats.DefaultKVStore(key)
		kvs, e := getKeyState(r.Context(), session, key)
		if e != nil {
			err = e
			return
		}

		opts := jsondiff.DefaultJSONOptions()

		for step := 0; ; step++ {
			diff, dstr := jsondiff.Compare(kvs.Value, p.Expected, &opts)
			if diff != jsondiff.FullMatch {
				res = KVCompareAndSwapResult{
					OK:       false,
					Diff:     json.RawMessage(dstr),
					Revision: kvs.Revision,
					Value:    kvs.Value,
				}
				return res, nil
			} else {
				rev, updateerr := kv.Update(r.Context(), key, p.Desired, kvs.Revision)
				if updateerr != nil {
					getv, geterr := kv.Get(r.Context(), key)

					// if we can't get the key, return the error
					if geterr != nil {
						err = geterr
						return
					}

					// someone else updated the key, retry
					if step < 16 && getv.Revision() != kvs.Revision {
						kvs.Revision = getv.Revision()
						kvs.Value = getv.Value()
						continue
					}

					// if we can't update the key, return the error
					err = updateerr
					return
				}

				res = KVCompareAndSwapResult{
					OK:       true,
					Diff:     json.RawMessage(dstr),
					Revision: rev,
					Value:    p.Desired,
				}
				return res, nil
			}
		}
	})
	Match("GET /kv/{key}", func(session *Session, r *http.Request, _ struct{}) (res KVState, err error) {
		key := r.PathValue("key")
		return getKeyState(r.Context(), session, key)
	})

	Match("PUT /kv/{key}", func(session *Session, r *http.Request, p KVSetIf) (res uint64, err error) {
		key := r.PathValue("key")
		kv := session.Nats.DefaultKVStore(key)
		var v uint64
		if p.Revision != 0 {
			v, err = kv.Update(r.Context(), key, p.Value, p.Revision)
		} else {
			v, err = kv.Create(r.Context(), key, p.Value)
		}
		if err != nil {
			return
		}
		return v, nil
	})

	Match("POST /kv/{key}", func(session *Session, r *http.Request, p json.RawMessage) (res uint64, err error) {
		key := r.PathValue("key")
		kv := session.Nats.DefaultKVStore(key)
		res, err = kv.Put(r.Context(), key, p)
		return
	})

	Match("DELETE /kv/{key}", func(session *Session, r *http.Request, _ struct{}) (_ struct{}, err error) {
		key := r.PathValue("key")
		kv := session.Nats.DefaultKVStore(key)
		err = kv.Purge(r.Context(), key)
		return
	})
	addKvList := func(path string, get func(session *Session) jetstream.KeyValue) {
		Match(path, func(session *Session, r *http.Request, _ struct{}) (res []string, err error) {
			kv := get(session)
			res, err = kv.Keys(r.Context())
			if err == jetstream.ErrNoKeysFound {
				err = nil
				res = []string{}
			}
			return
		})
	}
	addKvList("GET /kv", func(session *Session) jetstream.KeyValue { return session.Nats.DefaultKV })
	addKvList("GET /kv/d", func(session *Session) jetstream.KeyValue { return session.Nats.DailyKV })
	addKvList("GET /kv/w", func(session *Session) jetstream.KeyValue { return session.Nats.WeeklyKV })
	addKvList("GET /kv/m", func(session *Session) jetstream.KeyValue { return session.Nats.MonthlyKV })
}
