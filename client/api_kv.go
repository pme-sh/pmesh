package client

import (
	"encoding/json"
	"fmt"

	"get.pme.sh/pmesh/session"
)

func (c Client) KV(key string) (res session.KVState, err error) {
	err = c.Call("GET /kv/"+key, nil, &res)
	return
}
func (c Client) KVSet(key string, value any, revision uint64) (res uint64, err error) {
	v, err := json.Marshal(value)
	if err != nil {
		return
	}
	err = c.Call("PUT /kv/"+key, session.KVSetIf{Value: v, Revision: revision}, &res)
	return
}
func (c Client) KVCreate(key string, value any) (res uint64, err error) {
	v, err := json.Marshal(value)
	if err != nil {
		return
	}
	err = c.Call("PUT /kv/"+key, session.KVSetIf{Value: v}, &res)
	return
}
func (c Client) KVPut(key string, value any) (res uint64, err error) {
	err = c.Call("POST /kv/"+key, value, &res)
	return
}
func (c Client) KVCAS(key string, expected, desired any) (res session.KVCompareAndSwapResult, err error) {
	ex, err := json.Marshal(expected)
	if err != nil {
		return
	}
	de, err := json.Marshal(desired)
	if err != nil {
		return
	}
	err = c.Call("POST /kv/"+key+"/cas", session.KVCompareAndSwap{Expected: ex, Desired: de}, &res)
	return
}
func (c Client) KVPurge(key string) (res struct{}, err error) {
	err = c.Call("DELETE /kv/"+key, nil, &res)
	return
}
func (c Client) KVList() (res []string, err error) {
	err = c.Call("GET /kv", nil, &res)
	return
}
func (c Client) Result(stream string, seq int) (res json.RawMessage, err error) {
	path := fmt.Sprintf("/result/%s/%d", stream, seq)
	err = c.Call(path, nil, &res)
	return
}
