// Copyright 2023 Buf Technologies, Inc.
//
// All rights reserved.

package vanguard

import (
	"bytes"
	"testing"

	v1 "github.com/bufbuild/vanguard/internal/gen/library/v1"
	"github.com/google/go-cmp/cmp"
)

func TestBuffers(t *testing.T) {
	book := v1.Book{
		Name: "books/1",
	}

	bookJSON, err := codecJSON{}.MarshalAppend(nil, &book)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(bookJSON))
}

func TestEnvelopeChunker(t *testing.T) {
	book := v1.Book{
		Name: "books/1",
	}
	bookJSON, err := codecJSON{}.MarshalAppend(nil, &book)
	if err != nil {
		t.Fatal(err)
	}
	buffer := &bytes.Buffer{}
	buffer.WriteByte(0)
	putUint32(buffer, uint32(len(bookJSON)))
	buffer.Write(bookJSON)
	msg := buffer.Bytes()

	tests := []struct {
		name    string
		inputs  [][]byte
		outputs [][]byte
		chunks  [][]byte
		onChunk func([]byte) ([]byte, error)
	}{{
		name:    "single message",
		inputs:  [][]byte{msg},
		outputs: [][]byte{msg},
		chunks:  [][]byte{msg},
		onChunk: func(msg []byte) ([]byte, error) { return msg, nil },
	}, {
		name:    "partial message",
		inputs:  [][]byte{msg[:len(msg)/2], msg[len(msg)/2:]},
		outputs: [][]byte{{}, msg},
		chunks:  [][]byte{msg},
		onChunk: func(msg []byte) ([]byte, error) { return msg, nil },
	}, {
		name:    "partial envelope",
		inputs:  [][]byte{msg[:3], msg[3:]},
		outputs: [][]byte{{}, msg},
		chunks:  [][]byte{msg},
		onChunk: func(msg []byte) ([]byte, error) { return msg, nil },
	}, {
		name:    "expanding",
		inputs:  [][]byte{msg[:3], msg[3:]},
		outputs: [][]byte{{}, msg},
		chunks:  [][]byte{msg},
		onChunk: func(msg []byte) ([]byte, error) { return msg, nil },
	}}
	for _, testcase := range tests {
		testcase := testcase
		t.Run(testcase.name, func(t *testing.T) {
			chunks := [][]byte{}
			chunker := &envelopeChunker{
				buffer: &bytes.Buffer{},
				onChunk: func(msg []byte) ([]byte, error) {
					chunks = append(chunks, msg)
					return testcase.onChunk(msg)
				},
			}
			outputs := [][]byte{}
			for n, input := range testcase.inputs {
				var data bytes.Buffer
				data.Write(input)
				endStream := n == len(testcase.inputs)-1 // isEnd
				if err := chunker.Next(&data, endStream); err != nil {
					t.Fatal(err)
				}
				outputs = append(outputs, data.Bytes())
			}
			if diff := cmp.Diff(outputs, testcase.outputs); diff != "" {
				t.Error("outputs", diff)
			}
			if diff := cmp.Diff(chunks, testcase.chunks); diff != "" {
				t.Error("chunks", diff)
			}
		})
	}
}
