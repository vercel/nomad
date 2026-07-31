// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

//go:build linux

package cgroupslib

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shoenig/test/must"
)

func TestLifeCG2_Teardown(t *testing.T) {
	t.Run("accepts missing task cgroup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing.run-cell.scope")

		lifecycle := &lifeCG2{dpath: path, task: "run-cell"}
		must.NoError(t, lifecycle.Teardown())
	})

	t.Run("removes ordinary task cgroup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "alloc.other.scope")
		must.NoError(t, os.Mkdir(path, 0o755))

		lifecycle := &lifeCG2{dpath: path, task: "other"}
		must.NoError(t, lifecycle.Teardown())
		_, err := os.Stat(path)
		must.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("removes run-cell without delegated child", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "alloc.run-cell.scope")
		must.NoError(t, os.Mkdir(path, 0o755))
		must.NoError(t, os.Mkdir(filepath.Join(path, "unknown"), 0o755))

		lifecycle := &lifeCG2{dpath: path, task: "run-cell"}
		must.NoError(t, lifecycle.Teardown())
		_, err := os.Stat(path)
		must.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("does not preserve regular file named as delegated child", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "alloc.run-cell.scope")
		must.NoError(t, os.Mkdir(path, 0o755))
		must.NoError(t, os.WriteFile(filepath.Join(path, "manager"), nil, 0o644))

		lifecycle := &lifeCG2{dpath: path, task: "run-cell"}
		must.NoError(t, lifecycle.Teardown())
		_, err := os.Stat(path)
		must.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("requires canonical run-cell scope", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "run-cell")
		must.NoError(t, os.Mkdir(path, 0o755))
		must.NoError(t, os.Mkdir(filepath.Join(path, "manager"), 0o755))

		lifecycle := &lifeCG2{dpath: path, task: "run-cell"}
		must.NoError(t, lifecycle.Teardown())
		_, err := os.Stat(path)
		must.ErrorIs(t, err, os.ErrNotExist)
	})

	for _, child := range []string{"manager", "firecracker", "network", "storage"} {
		t.Run("preserves delegated "+child, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "alloc.run-cell.scope")
			childPath := filepath.Join(path, child)
			must.NoError(t, os.Mkdir(path, 0o755))
			must.NoError(t, os.Mkdir(childPath, 0o755))

			lifecycle := &lifeCG2{dpath: path, task: "run-cell"}
			must.NoError(t, lifecycle.Teardown())
			_, err := os.Stat(childPath)
			must.NoError(t, err)

			must.NoError(t, os.Remove(childPath))
			must.NoError(t, lifecycle.Teardown())
			_, err = os.Stat(path)
			must.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
