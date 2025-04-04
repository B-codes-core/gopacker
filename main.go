package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/nirhaas/gopacker/lib"
	"github.com/pkg/errors"
)

const (
	usageString       = "USAGE: gopacker <executable_path>"
	packedExt         = ".packed"
	footerMagicString = "LALALALA"
)

var (
	compression  = lib.ZSTDCompression{}
	footerMagic  = []byte(footerMagicString)
	selfPath     string
	inputPath    string
	packedPath   string
	unpackedPath string
)

type counterWriter struct {
	io.Writer
	n int
}

func (w *counterWriter) Write(buf []byte) (n int, err error) {
	n, err = w.Writer.Write(buf)
	w.n += n
	return n, err
}

func appendToFile(dst string, in io.Reader, compress bool) (n int64, err error) {
	_, err = os.Stat(dst)
	if err != nil {
		return 0, errors.Wrap(err, "destination file does not exist")
	}

	destination, err := os.OpenFile(dst, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return 0, errors.Wrap(err, "failed opening destination file")
	}
	defer destination.Close()

	if compress {
		cw := &counterWriter{Writer: destination}
		_, err := lib.CompressStream(compression, cw, in)
		return int64(cw.n), errors.Wrap(err, "failed appending compressed to destination")
	}

	return io.Copy(destination, in)
}

func copyFile(src, dst string) (err error) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return errors.Wrap(err, "src file does not exist")
	}
	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return errors.Wrap(err, "failed opening source file")
	}
	defer source.Close()

	destination, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return errors.Wrap(err, "failed opening destination file")
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func mainCLI() {
	if len(os.Args) == 1 {
		fmt.Println(usageString)
		return
	}

	inputPath = os.Args[1]
	packedPath = os.Args[1] + packedExt

	if _, err := os.Stat(os.Args[1]); err != nil {
		log.Fatal(errors.Wrap(err, "target exec does not exist"))
	}

	if err := copyFile(selfPath, packedPath); err != nil {
		log.Fatal(errors.Wrap(err, "failed copying stub"))
	}

	packedPathHandle, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed opening packed path"))
	}
	defer packedPathHandle.Close()

	bytesWritten, err := appendToFile(packedPath, packedPathHandle, true)
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed appending stub"))
	}

	bs := make([]byte, 8)
	binary.LittleEndian.PutUint64(bs, uint64(bytesWritten))
	if _, err := appendToFile(packedPath, bytes.NewReader(bs), false); err != nil {
		log.Fatal(errors.Wrap(err, "failed appending len"))
	}

	if _, err := appendToFile(packedPath, bytes.NewReader(footerMagic), false); err != nil {
		log.Fatal(errors.Wrap(err, "failed appending magic"))
	}
}

func mainStub(selfFileHandle *os.File, fileSize int64) {
	unpackedPath = selfPath + "_unpacked.exe" // Avoid self-overwriting

	dstLen := make([]byte, 8)
	if _, err := selfFileHandle.Seek(fileSize-int64(len(footerMagic))-int64(len(dstLen)), 0); err != nil {
		log.Fatal(errors.Wrap(err, "failed seeking length"))
	}
	if _, err := selfFileHandle.Read(dstLen); err != nil {
		log.Fatal(errors.Wrap(err, "failed reading length"))
	}
	targetLen := int64(binary.LittleEndian.Uint64(dstLen))

	var buf bytes.Buffer
	compressedOff := fileSize - int64(len(footerMagic)) - int64(len(dstLen)) - targetLen
	compressedReader := io.NewSectionReader(selfFileHandle, compressedOff, targetLen)
	if _, err := lib.DecompressStream(compression, &buf, compressedReader); err != nil {
		log.Fatal(errors.Wrap(err, "failed decompressing file"))
	}

	if err := ioutil.WriteFile(unpackedPath, buf.Bytes(), 0755); err != nil {
		log.Fatal(errors.Wrap(err, "failed writing unpacked file"))
	}

	cmd := exec.Command(unpackedPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatal(errors.Wrap(err, "failed running unpacked executable"))
	}

	// Optional: Remove unpacked executable after execution
	go func() {
		cmd.Wait()
		os.Remove(unpackedPath)
	}()
}

func main() {
	var err error
	selfPath, err = os.Executable() // Get absolute path
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed expanding self path"))
	}
	selfPath, err = filepath.Abs(selfPath) // Convert to absolute path
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed getting absolute path"))
	}

	selfFileStat, err := os.Stat(selfPath)
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed stat self file"))
	}

	selfFileHandle, err := os.Open(selfPath)
	if err != nil {
		log.Fatal(errors.Wrap(err, "failed opening self file"))
	}
	defer selfFileHandle.Close()

	dstMagic := make([]byte, len(footerMagic))
	if _, err := selfFileHandle.Seek(selfFileStat.Size()-int64(len(footerMagic)), 0); err != nil {
		log.Fatal(errors.Wrap(err, "failed seeking magic"))
	}
	if _, err := selfFileHandle.Read(dstMagic); err != nil {
		log.Fatal(errors.Wrap(err, "failed reading magic"))
	}

	if !bytes.Equal(footerMagic, dstMagic) {
		mainCLI()
		return
	}

	mainStub(selfFileHandle, selfFileStat.Size())
}
