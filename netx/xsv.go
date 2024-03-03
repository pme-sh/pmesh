package netx

import (
	"bufio"
	"bytes"
	"encoding"
	"errors"
	"io"
	"reflect"
	"strconv"

	"github.com/pme-sh/pmesh/util"

	"github.com/samber/lo"
)

type structDecoder struct {
	typeId reflect.Type
	fields []func(into reflect.Value, value []byte) error
}

type splitter struct {
	data    []byte
	sep     byte
	scanner *bufio.Scanner
}

func (s *splitter) scan() (bool, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return false, err
		}
		return false, io.EOF
	}
	s.data = s.scanner.Bytes()
	return true, nil
}
func (s *splitter) collect() []string {
	var res []string
	for {
		str, ok := s.next()
		if !ok {
			break
		}
		res = append(res, string(str))
	}
	return res
}
func (s *splitter) next() ([]byte, bool) {
	if len(s.data) == 0 {
		return nil, false
	}
	end := bytes.IndexByte(s.data, s.sep)
	if end == -1 {
		str := s.data
		s.data = nil
		return str, true
	} else {
		str := s.data[:end]
		s.data = s.data[end+1:]
		return str, true
	}
}

func (sd *structDecoder) decode(value reflect.Value, s *splitter) {
	for _, dec := range sd.fields {
		str, _ := s.next()
		if dec != nil {
			dec(value, str)
		}
	}
}
func newStructDecoder(ty reflect.Type, hdr []string) (sd *structDecoder) {
	sd = &structDecoder{
		typeId: ty,
		fields: make([]func(into reflect.Value, value []byte) error, len(hdr)),
	}

	for _, f := range reflect.VisibleFields(ty) {
		tag := f.Tag.Get("xsv")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		idx := lo.IndexOf(hdr, tag)
		if idx == -1 {
			panic("missing header: " + tag)
		}

		f := f
		fieldIdx := f.Index[0]
		if len(f.Index) > 1 {
			panic("nested fields are not supported")
		}
		var hnd func(into reflect.Value, value []byte) error
		switch f.Type.Kind() {
		case reflect.String:
			hnd = func(into reflect.Value, value []byte) error {
				into.Field(fieldIdx).SetString(string(value))
				return nil
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			hnd = func(into reflect.Value, bvalue []byte) error {
				value := util.UnsafeString(bvalue)
				i, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return err
				}
				into.Field(fieldIdx).SetInt(i)
				return nil
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			hnd = func(into reflect.Value, bvalue []byte) error {
				value := util.UnsafeString(bvalue)
				i, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return err
				}
				into.Field(fieldIdx).SetUint(i)
				return nil
			}
		case reflect.Float32, reflect.Float64:
			hnd = func(into reflect.Value, bvalue []byte) error {
				value := util.UnsafeString(bvalue)
				i, err := strconv.ParseFloat(value, 64)
				if err != nil {
					return err
				}
				into.Field(fieldIdx).SetFloat(i)
				return nil
			}
		case reflect.Bool:
			hnd = func(into reflect.Value, bvalue []byte) error {
				value := util.UnsafeString(bvalue)
				i, err := strconv.ParseBool(value)
				if err != nil {
					return err
				}
				into.Field(fieldIdx).SetBool(i)
				return nil
			}
		default:
		}
		if hnd == nil {
			switch f.Type {
			case reflect.TypeOf((*IP)(nil)).Elem():
				hnd = func(into reflect.Value, bvalue []byte) error {
					value := util.UnsafeString(bvalue)
					into.Field(fieldIdx).Set(reflect.ValueOf(ParseIP(value)))
					return nil
				}
			default:
				if reflect.PointerTo(f.Type).Implements(reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()) {
					hnd = func(into reflect.Value, value []byte) error {
						unmarshaler := into.Field(fieldIdx).Addr().Interface().(encoding.TextUnmarshaler)
						return unmarshaler.UnmarshalText(value)
					}
				} else {
					panic("unsupported type: " + f.Type.String())
				}
			}
		}
		sd.fields[idx] = hnd
	}
	return
}

type xsvDecoder struct {
	Header   []string       // Column names.
	decoder  *structDecoder // Decoder for the struct.
	splitter *splitter
}

func newXsvDecoder(r io.Reader, sep string, hdr ...string) *xsvDecoder {
	if len(sep) != 1 {
		panic("separator must be a single character")
	}
	return &xsvDecoder{
		Header:   hdr,
		splitter: &splitter{sep: sep[0], scanner: bufio.NewScanner(r)},
	}
}
func (dec *xsvDecoder) WithHeader(h ...string) *xsvDecoder {
	dec.Header = h
	return dec
}

func (dec *xsvDecoder) Decode(v any) error {
	if len(dec.Header) == 0 {
		_, err := dec.splitter.scan()
		if err != nil {
			return err
		}
		dec.Header = dec.splitter.collect()
	}
	_, err := dec.splitter.scan()
	if err != nil {
		return err
	}

	value := reflect.ValueOf(v)
	if value.Kind() != reflect.Ptr {
		return errors.New("v must be a pointer")
	}
	value = value.Elem()
	if value.Kind() != reflect.Struct {
		return errors.New("v must be a pointer to a struct")
	}
	if dec.decoder == nil || dec.decoder.typeId != value.Type() {
		dec.decoder = newStructDecoder(value.Type(), dec.Header)
	}
	dec.decoder.decode(value, dec.splitter)
	return nil
}
