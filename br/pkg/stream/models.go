// Copyright 2021 PingCAP, Inc. Licensed under Apache-2.0.
package stream

import (
	"bytes"
	"encoding/binary"
	"path"
	"regexp"
	"strings"

	"github.com/pingcap/errors"
	backuppb "github.com/pingcap/kvproto/pkg/brpb"
	berrors "github.com/pingcap/tidb/br/pkg/errors"
	"github.com/pingcap/tidb/kv"
)

const (
	// The commmon prefix of keys involved by the stream backup.
	streamKeyPrefix    = "/tidb/br-stream"
	taskInfoPath       = "/info"
	taskCheckpointPath = "/checkpoint"
	taskRangesPath     = "/ranges"
	taskPausePath      = "/pause"
)

var (
	taskNameRe = regexp.MustCompile(`^[0-9a-zA-Z_]+$`)
)

// TaskOf returns the path of tasks.
// Normally it would be <prefix>/info/<task-name(string)> -> <task(protobuf)>
func TaskOf(name string) string {
	return path.Join(streamKeyPrefix, taskInfoPath, name)
}

// RangesOf returns the path prefix for some task.
// Normally it would be <prefix>/ranges/<task-name(string)>/
// the trailling slash is essential or we may scan ranges of tasks with same prefix.
func RangesOf(name string) string {
	return path.Join(streamKeyPrefix, taskRangesPath, name)
}

// RangeKeyOf returns the path for ranges of some task.
// Normally it would be <prefix>/ranges/<task-name(string)>/<start-key(binary)> -> <end-key(binary)>
func RangeKeyOf(name string, startKey []byte) string {
	// We cannot use path.Join if the start key contains patterns like [0x2f, 0x2f](//).
	return RangesOf(name) + string(startKey)
}

func writeUint64(buf *bytes.Buffer, num uint64) {
	items := [8]byte{}
	binary.BigEndian.PutUint64(items[:], num)
	buf.Write(items[:])
}

func encodeUint64(num uint64) []byte {
	items := [8]byte{}
	binary.BigEndian.PutUint64(items[:], num)
	return items[:]
}

// CheckpointOf returns the checkpoint of some task, in some region of some store.
// All binary u64 are encoded as big endian.
// Normally it would be <prefix>/checkpoint/<store-id(binary-u64)>/<region-id(binary-u64)> -> <next-backup-ts(binary-u64)>
func CheckpointOf(task string, store uint64, region uint64) string {
	buf := bytes.NewBuffer(nil)
	buf.WriteString(strings.TrimSuffix(path.Join(streamKeyPrefix, task), "/"))
	buf.WriteRune('/')
	writeUint64(buf, store)
	buf.WriteRune('/')
	writeUint64(buf, region)
	return buf.String()
}

// CheckpointsOf returns the checkpoint prefix of some store.
// Normally it would be <prefix>/checkpoint/<store-id(binary-u64)>.
func CheckPointsOf(task string, store uint64) string {
	buf := bytes.NewBuffer(nil)
	buf.WriteString(strings.TrimSuffix(path.Join(streamKeyPrefix, task), "/"))
	buf.WriteRune('/')
	writeUint64(buf, store)
	buf.WriteRune('/')
	return buf.String()
}

// Pause returns the path for pausing the task.
func Pause(task string) string {
	return path.Join(streamKeyPrefix, task, taskPausePath)
}

// Ranges is a vector of [start_key, end_key) pairs.
type Ranges = []Range
type Range = kv.KeyRange

// TaskInfo is a task info with extra information.
type TaskInfo struct {
	PBInfo  backuppb.StreamBackupTaskInfo
	Ranges  Ranges
	Pausing bool
}

// NewTask creates a new task with the name.
func NewTask(name string) *TaskInfo {
	return &TaskInfo{
		PBInfo: backuppb.StreamBackupTaskInfo{
			Name: name,
		},
	}
}

// WithRange adds a backup range to the task, and return itself.
func (t *TaskInfo) WithRange(startKey, endKey []byte) *TaskInfo {
	t.Ranges = append(t.Ranges, Range{
		StartKey: startKey,
		EndKey:   endKey,
	})
	return t
}

// WithRanges adds some ranges to the task, and return itself.
func (t *TaskInfo) WithRanges(ranges ...Range) *TaskInfo {
	t.Ranges = append(t.Ranges, ranges...)
	return t
}

// FromTS set the initial version of the stream backup, and return itself.
func (t *TaskInfo) FromTS(ts uint64) *TaskInfo {
	t.PBInfo.StartTs = ts
	return t
}

// UntilTS set the terminal version of the stream backup, and return itself.
func (t *TaskInfo) UntilTS(ts uint64) *TaskInfo {
	t.PBInfo.EndTs = ts
	return t
}

// WithTableFilterHint adds the table filter of the stream backup, and return itself.
// When schama version changed, TiDB should change the ranges of the task accroding to the table filter.
func (t *TaskInfo) WithTableFilter(filterChain ...string) *TaskInfo {
	t.PBInfo.TableFilter = filterChain
	return t
}

// ToStorage indicates the backup task to the external storage.
func (t *TaskInfo) ToStorage(backend *backuppb.StorageBackend) *TaskInfo {
	t.PBInfo.Storage = backend
	return t
}

func (t *TaskInfo) Check() (*TaskInfo, error) {
	if t.PBInfo.Storage == nil {
		return nil, errors.Annotate(berrors.ErrPiTRInvalidTaskInfo, "the storage backend is null")
	}
	if len(t.PBInfo.TableFilter) == 0 {
		return nil, errors.Annotate(berrors.ErrPiTRInvalidTaskInfo, "the table filter is empty, maybe add '*.*' for including all tables")
	}
	// Maybe check StartTs > 0?
	if !taskNameRe.MatchString(t.PBInfo.Name) {
		return nil, errors.Annotatef(berrors.ErrPiTRInvalidTaskInfo,
			"the task name can contain alphanumeric characters and underscore only (re = %s, name = %s)",
			taskNameRe.String(), t.PBInfo.Name)
	}
	// Maybe check whether the ranges overlapped?
	return t, nil
}
