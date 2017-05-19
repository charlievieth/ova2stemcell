package main

import (
	"bytes"
	"crypto/md5"
	"errors"
	"io"

	"bitbucket.org/kardianos/rsync"
	"bitbucket.org/kardianos/rsync/proto"
)

var NoTargetSumError = errors.New("Checksum request but missing target hash.")
var HashNoMatchError = errors.New("Final data hash does not match.")

func Patch(basis io.ReadSeeker, delta io.Reader, newfile io.Writer) error {
	var rs rsync.RSync // use defaults

	deltaDecode := proto.Reader{
		Reader: delta,
	}
	defer deltaDecode.Close()

	bs, err := deltaDecode.Header(proto.TypeDelta)
	if err != nil {
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	rs.BlockSize = bs

	hashOps := make(chan rsync.Operation, 2)
	ops := make(chan rsync.Operation)
	// Load operations from file.
	var decodeError error
	go func() {
		defer close(ops)
		decodeError = deltaDecode.ReadOperations(ops, hashOps)
	}()

	// apply patch
	hasher := md5.New()
	if err := rs.ApplyDelta(newfile, basis, ops, hasher); err != nil {
		return err
	}
	if decodeError != nil {
		return decodeError
	}

	// check hash
	hashOp := <-hashOps
	if hashOp.Data == nil {
		return NoTargetSumError
	}
	if bytes.Equal(hashOp.Data, hasher.Sum(nil)) == false {
		return HashNoMatchError
	}

	return nil
}
