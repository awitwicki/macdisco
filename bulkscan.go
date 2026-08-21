package main

import (
	"encoding/binary"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// getattrlistbulk(2) is not wrapped by x/sys; call it directly. Syscall
// number 461, stable Darwin ABI since 10.10 (the amd64 asm shim adds the
// 0x2000000 BSD class offset itself, arm64 takes the plain number).
const sysGetattrlistbulk = 461

func getattrlistbulk(fd int, list *unix.Attrlist, buf []byte, options uint64) (int, error) {
	n, _, errno := syscall.Syscall6(sysGetattrlistbulk,
		uintptr(fd),
		uintptr(unsafe.Pointer(list)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(options), 0)
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

// Fast directory reader built on getattrlistbulk(2): one syscall returns
// name, type, sizes, link count and inode for a whole batch of entries,
// instead of readdir + one lstat per file. This is what makes scanning
// millions of files take seconds on APFS.

// vnode types from sys/vnode.h
const (
	vREG = 1
	vDIR = 2
	vLNK = 5
)

type bulkEntry struct {
	name  string
	isDir bool
	size  int64  // logical data length
	alloc int64  // allocated on disk
	nlink uint32
	dev   uint32
	ino   uint64
}

var bulkBufPool = sync.Pool{
	New: func() any { b := make([]byte, 128*1024); return &b },
}

// readDirBulk lists a directory via getattrlistbulk. ok=false means the
// filesystem does not support it and the caller must fall back to lstat.
// A nil entries slice with ok=true and err!=nil means the dir is unreadable.
func readDirBulk(path string) (entries []bulkEntry, ok bool, err error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, true, err
	}
	defer unix.Close(fd)

	attrs := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_NAME |
			unix.ATTR_CMN_DEVID | unix.ATTR_CMN_OBJTYPE | unix.ATTR_CMN_FILEID,
		Fileattr: unix.ATTR_FILE_LINKCOUNT | unix.ATTR_FILE_ALLOCSIZE |
			unix.ATTR_FILE_DATALENGTH,
	}

	bufp := bulkBufPool.Get().(*[]byte)
	defer bulkBufPool.Put(bufp)
	buf := *bufp

	for {
		n, err := getattrlistbulk(fd, &attrs, buf, 0)
		if err != nil {
			// ENOTSUP/EINVAL: filesystem without bulk support (NFS, SMB, ...)
			return nil, false, err
		}
		if n == 0 {
			return entries, true, nil
		}
		off := 0
		for i := 0; i < n; i++ {
			entryLen := int(binary.LittleEndian.Uint32(buf[off:]))
			if entryLen < 24 || off+entryLen > len(buf) {
				return nil, false, nil // malformed, use the safe path
			}
			e, parsed := parseBulkEntry(buf[off+4 : off+entryLen])
			off += entryLen
			if !parsed {
				return nil, false, nil
			}
			if e.name != "" {
				entries = append(entries, e)
			}
		}
	}
}

// parseBulkEntry decodes one packed attribute record. Attributes appear in
// the fixed order defined by getattrlist(2): common attrs in bit order, then
// file attrs in bit order, each present only if set in the returned bitmap.
func parseBulkEntry(fields []byte) (e bulkEntry, ok bool) {
	// returned attribute_set_t: common, vol, dir, file, fork bitmaps
	retCommon := binary.LittleEndian.Uint32(fields[0:])
	retFile := binary.LittleEndian.Uint32(fields[12:])
	p := 20
	var objType uint32

	if retCommon&unix.ATTR_CMN_NAME != 0 {
		nameOff := int(int32(binary.LittleEndian.Uint32(fields[p:])))
		nameLen := int(binary.LittleEndian.Uint32(fields[p+4:]))
		start := p + nameOff
		if start < 0 || nameLen < 1 || start+nameLen > len(fields) {
			return e, false
		}
		e.name = string(fields[start : start+nameLen-1]) // drop NUL
		p += 8
	}
	if retCommon&unix.ATTR_CMN_DEVID != 0 {
		e.dev = binary.LittleEndian.Uint32(fields[p:])
		p += 4
	}
	if retCommon&unix.ATTR_CMN_OBJTYPE != 0 {
		objType = binary.LittleEndian.Uint32(fields[p:])
		p += 4
	}
	if retCommon&unix.ATTR_CMN_FILEID != 0 {
		e.ino = binary.LittleEndian.Uint64(fields[p:])
		p += 8
	}
	if retFile&unix.ATTR_FILE_LINKCOUNT != 0 {
		e.nlink = binary.LittleEndian.Uint32(fields[p:])
		p += 4
	}
	if retFile&unix.ATTR_FILE_ALLOCSIZE != 0 {
		e.alloc = int64(binary.LittleEndian.Uint64(fields[p:]))
		p += 8
	}
	if retFile&unix.ATTR_FILE_DATALENGTH != 0 {
		e.size = int64(binary.LittleEndian.Uint64(fields[p:]))
	}
	e.isDir = objType == vDIR
	return e, true
}

// bulkDisabled lets users force the portable lstat scanner.
var bulkDisabled = os.Getenv("MACDISCO_NO_BULK") != ""
