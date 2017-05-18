package rdiff

import (
	"bytes"
	"crypto/md5"
	"errors"
	"hash"
	"io"
	"os"

	"bitbucket.org/kardianos/rsync"
	"bitbucket.org/kardianos/rsync/proto"
)

var NoTargetSumError = errors.New("Checksum request but missing target hash.")
var HashNoMatchError = errors.New("Final data hash does not match.")

func Patch(basis, delta, newfile string, checkFile bool) error {
	rs := getRsync()
	basisFile, err := os.Open(basis)
	if err != nil {
		return err
	}
	defer basisFile.Close()

	deltaFile, err := os.Open(delta)
	if err != nil {
		return err
	}
	defer deltaFile.Close()

	fsFile, err := os.Create(newfile)
	if err != nil {
		return err
	}
	defer fsFile.Close()

	deltaDecode := proto.Reader{Reader: deltaFile}
	rs.BlockSize, err = deltaDecode.Header(proto.TypeDelta)
	if err != nil {
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	defer deltaDecode.Close()

	hashOps := make(chan rsync.Operation, 2)
	ops := make(chan rsync.Operation)
	// Load operations from file.
	var decodeError error
	go func() {
		defer close(ops)
		decodeError = deltaDecode.ReadOperations(ops, hashOps)
	}()

	var hasher hash.Hash
	if checkFile {
		hasher = md5.New()
	}
	err = rs.ApplyDelta(fsFile, basisFile, ops, hasher)
	if err != nil {
		return err
	}
	if decodeError != nil {
		return decodeError
	}
	if checkFile == false {
		return nil
	}
	hashOp := <-hashOps
	if hashOp.Data == nil {
		return NoTargetSumError
	}
	if bytes.Equal(hashOp.Data, hasher.Sum(nil)) == false {
		return HashNoMatchError
	}

	return nil
}

func getRsync() *rsync.RSync {
	return &rsync.RSync{
		MaxDataOp: 1024 * 16,
	}
}
