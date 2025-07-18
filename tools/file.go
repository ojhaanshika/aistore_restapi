//nolint:usetesting // need mkdir(dir = bdir)
// Package tools provides common tools and utilities for all unit and integration tests
/*
 * Copyright (c) 2018-2025, NVIDIA CORPORATION. All rights reserved.
 */
package tools

import (
	"bytes"
	cryptorand "crypto/rand"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/NVIDIA/aistore/api/apc"
	"github.com/NVIDIA/aistore/cmn"
	"github.com/NVIDIA/aistore/cmn/cos"
	"github.com/NVIDIA/aistore/core"
	"github.com/NVIDIA/aistore/core/meta"
	"github.com/NVIDIA/aistore/core/mock"
	"github.com/NVIDIA/aistore/fs"
	"github.com/NVIDIA/aistore/tools/tassert"
	"github.com/NVIDIA/aistore/tools/trand"
)

type (
	DirTreeDesc struct {
		InitDir  string // Directory where the tree is created (can be empty).
		Dirs     int    // Number of (initially empty) directories at each depth (we recurse into single directory at each depth).
		Files    int    // Number of files at each depth.
		FileSize int64  // Size of each file.
		Depth    int    // Depth of tree/nesting.
		Empty    bool   // Determines if there is a file somewhere in the directories.
	}

	ContentTypeDesc struct {
		Type       string
		ContentCnt int
	}

	ObjectsDesc struct {
		CTs           []ContentTypeDesc // Content types which are interesting for the test.
		MountpathsCnt int               // Number of mountpaths to be created.
		ObjectSize    int64
	}

	ObjectsOut struct {
		Dir             string
		Bck             cmn.Bck
		FQNs            map[string][]string // ContentType => FQN
		MpathObjectsCnt map[string]int      // mpath -> # objects on the mpath
	}
)

func RandomObjDir(dirLen, maxDepth int) (dir string) {
	depth := rand.IntN(maxDepth)
	for range depth {
		dir = filepath.Join(dir, trand.String(dirLen))
	}
	return
}

func SetXattrCksum(fqn string, bck cmn.Bck, cksum *cos.Cksum) error {
	lom := &core.LOM{}
	// NOTE: this is an intentional hack to go ahead and corrupt the checksum
	//       - init and/or load errors are ignored on purpose
	_ = lom.InitFQN(fqn, &bck)
	_ = lom.LoadMetaFromFS()
	lom.SetCksum(cksum)
	return lom.Persist()
}

func ModifyLOM(t *testing.T, fqn string, bck cmn.Bck, modify func(*testing.T, *core.LOM)) {
	lom := &core.LOM{}
	// NOTE: this is an intentional hack to go ahead and corrupt the checksum
	//       - init and/or load errors are ignored on purpose
	_ = lom.InitFQN(fqn, &bck)
	_ = lom.LoadMetaFromFS()
	modify(t, lom)
	err := lom.Persist()
	tassert.CheckFatal(t, err)
}

func CheckPathExists(t *testing.T, path string, dir bool) {
	if fi, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else {
		if dir && !fi.IsDir() {
			t.Fatalf("expected path %q to be directory", path)
		} else if !dir && fi.IsDir() {
			t.Fatalf("expected path %q to not be directory", path)
		}
	}
}

func CheckPathNotExists(t *testing.T, path string) {
	if err := cos.Stat(path); err == nil || !cos.IsNotExist(err) {
		t.Fatal(err)
	}
}

func PrepareDirTree(tb testing.TB, desc DirTreeDesc) (string, []string) {
	fileNames := make([]string, 0, 100)
	topDirName, err := os.MkdirTemp(desc.InitDir, "")
	tassert.CheckFatal(tb, err)

	nestedDirectoryName := topDirName
	for depth := 1; depth <= desc.Depth; depth++ {
		names := make([]string, 0, desc.Dirs)
		for i := 1; i <= desc.Dirs; i++ {
			name, err := os.MkdirTemp(nestedDirectoryName, "")
			tassert.CheckFatal(tb, err)
			names = append(names, name)
		}
		for i := 1; i <= desc.Files; i++ {
			f, err := os.CreateTemp(nestedDirectoryName, "")
			tassert.CheckFatal(tb, err)
			if desc.FileSize > 0 {
				io.Copy(f, io.LimitReader(cryptorand.Reader, desc.FileSize))
			}
			fileNames = append(fileNames, f.Name())
			err = f.Close()
			tassert.CheckFatal(tb, err)
		}
		sort.Strings(names)
		if desc.Dirs > 0 {
			// We only recurse into last directory.
			nestedDirectoryName = names[len(names)-1]
		}
	}

	if !desc.Empty {
		f, err := os.CreateTemp(nestedDirectoryName, "")
		tassert.CheckFatal(tb, err)
		if desc.FileSize > 0 {
			io.Copy(f, io.LimitReader(cryptorand.Reader, desc.FileSize))
		}
		fileNames = append(fileNames, f.Name())
		err = f.Close()
		tassert.CheckFatal(tb, err)
	}
	return topDirName, fileNames
}

func PrepareObjects(t *testing.T, desc ObjectsDesc) *ObjectsOut {
	var (
		buf       = make([]byte, desc.ObjectSize)
		fqns      = make(map[string][]string, len(desc.CTs))
		mpathCnts = make(map[string]int, desc.MountpathsCnt)

		bck = cmn.Bck{
			Name:     trand.String(10),
			Provider: apc.AIS,
			Ns:       cmn.NsGlobal,
			Props: &cmn.Bprops{
				Cksum: cmn.CksumConf{Type: cos.ChecksumCesXxh},
				BID:   0xa5b6e7d8,
			},
		}
		bmd = mock.NewBaseBownerMock((*meta.Bck)(&bck))
	)

	mios := mock.NewIOS()
	fs.TestNew(mios)

	fs.CSM.Reg(fs.WorkfileType, &fs.WorkfileContentResolver{}, true)
	fs.CSM.Reg(fs.ObjectType, &fs.ObjectContentResolver{}, true)
	fs.CSM.Reg(fs.ECSliceType, &fs.ECSliceContentResolver{}, true)
	fs.CSM.Reg(fs.ECMetaType, &fs.ECMetaContentResolver{}, true)

	dir := t.TempDir()

	for range desc.MountpathsCnt {
		mpath, err := os.MkdirTemp(dir, "")
		tassert.CheckFatal(t, err)
		mp, err := fs.Add(mpath, "daeID")
		tassert.CheckFatal(t, err)
		mpathCnts[mp.Path] = 0
	}

	if len(desc.CTs) == 0 {
		return nil
	}

	core.T = mock.NewTarget(bmd) // a.k.a. tMock

	errs := fs.CreateBucket(&bck, false /*nilbmd*/)
	if len(errs) > 0 {
		tassert.CheckFatal(t, errs[0])
	}

	for _, ct := range desc.CTs {
		for range ct.ContentCnt {
			fqn, _, err := core.HrwFQN(&bck, ct.Type, trand.String(15))
			tassert.CheckFatal(t, err)

			fqns[ct.Type] = append(fqns[ct.Type], fqn)

			f, err := cos.CreateFile(fqn)
			tassert.CheckFatal(t, err)
			_, _ = cryptorand.Read(buf)
			_, err = f.Write(buf)
			f.Close()
			tassert.CheckFatal(t, err)

			var parsed fs.ParsedFQN
			err = parsed.Init(fqn)
			tassert.CheckFatal(t, err)
			mpathCnts[parsed.Mountpath.Path]++

			switch ct.Type {
			case fs.ObjectType:
				lom := &core.LOM{}
				err = lom.InitFQN(fqn, nil)
				tassert.CheckFatal(t, err)

				lom.SetSize(desc.ObjectSize)
				lom.SetAtimeUnix(time.Now().UnixNano())
				err = lom.Persist()
				tassert.CheckFatal(t, err)
			case fs.WorkfileType, fs.ECSliceType, fs.ECMetaType:
			default:
				cos.AssertMsg(false, "non-implemented type")
			}
		}
	}

	return &ObjectsOut{
		Dir:             dir,
		Bck:             bck,
		FQNs:            fqns,
		MpathObjectsCnt: mpathCnts,
	}
}

func PrepareMountPaths(t *testing.T, cnt int) fs.MPI {
	PrepareObjects(t, ObjectsDesc{
		MountpathsCnt: cnt,
	})
	AssertMountpathCount(t, cnt, 0)
	return fs.GetAvail()
}

func RemoveMpaths(t *testing.T, mpaths fs.MPI) {
	for _, mpath := range mpaths {
		removedMP, err := fs.Remove(mpath.Path)
		tassert.CheckError(t, err)
		tassert.Errorf(t, removedMP != nil, "expected remove to be successful")
		tassert.CheckError(t, fs.RemoveAll(mpath.Path))
	}
}

func AddMpath(t *testing.T, path string) {
	err := cos.CreateDir(path) // Create directory if not exists
	tassert.CheckFatal(t, err)
	t.Cleanup(func() {
		fs.RemoveAll(path)
	})
	_, err = fs.Add(path, "daeID")
	tassert.Errorf(t, err == nil, "Failed adding mountpath %q, err: %v", path, err)
}

func AssertMountpathCount(t *testing.T, na, nd int) {
	var (
		avail, disabled = fs.Get()
		la, ld          = len(avail), len(disabled)
	)
	if la != na || ld != nd {
		t.Errorf("wrong mountpath count: avail (have %d, expect %d), disabled (have %d, expect %d)", la, na, ld, nd)
	}
}

func CreateFileFromReader(t *testing.T, fileName string, r io.Reader) string {
	filePath := filepath.Join(t.TempDir(), fileName)
	f, err := os.Create(filePath)
	tassert.CheckFatal(t, err)

	_, err = io.Copy(f, r)
	tassert.CheckFatal(t, err)

	err = f.Close()
	tassert.CheckFatal(t, err)

	return filePath
}

func FilesEqual(file1, file2 string) (bool, error) {
	f1, err := os.ReadFile(file1)
	if err != nil {
		return false, err
	}
	f2, err := os.ReadFile(file2)
	if err != nil {
		return false, err
	}
	return bytes.Equal(f1, f2), nil
}

func ReaderEqual(r1, r2 io.Reader) bool {
	buf1 := new(bytes.Buffer)
	buf2 := new(bytes.Buffer)

	_, err1 := buf1.ReadFrom(r1)
	_, err2 := buf2.ReadFrom(r2)

	if err1 != nil || err2 != nil {
		return false
	}

	return bytes.Equal(buf1.Bytes(), buf2.Bytes())
}
