package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"neuroforge/internal/core"
)

const v161JobCompactionMarker = ".migration-v1.6.1-terminal-relink-compaction"

// compactLegacyTerminalRelinkCheckpoint is a one-time, streaming migration for
// v1.6.0 checkpoints. That release retained full target/candidate vectors in
// completed vector.relink jobs. A large graph backfill can therefore make
// state.json hundreds of MB or larger and cause OOM during the next startup.
//
// The migration deliberately runs before state.json is unmarshaled. It rewrites
// JSON token-by-token and only materializes one Job at a time, so peak memory is
// bounded by the largest single job instead of the whole checkpoint.
func compactLegacyTerminalRelinkCheckpoint(dir string) (int, error) {
	marker := filepath.Join(dir, v161JobCompactionMarker)
	if _, err := os.Stat(marker); err == nil {
		return 0, nil
	}
	path := filepath.Join(dir, "state.json")
	in, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fresh data directory. Do not create the migration marker yet: an
			// operator may restore a v1.6.0 checkpoint into this directory before
			// the next boot, and that restored checkpoint must still be compacted.
			return 0, nil
		}
		return 0, err
	}
	defer in.Close()

	tmp := path + ".v161-compact.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()

	dec := json.NewDecoder(bufio.NewReaderSize(in, 1<<20))
	dec.UseNumber()
	bw := bufio.NewWriterSize(out, 1<<20)
	compacted, err := rewriteCheckpointObject(dec, bw)
	if err != nil {
		return 0, err
	}
	if err := bw.Flush(); err != nil {
		return 0, err
	}
	if err := out.Sync(); err != nil {
		return 0, err
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	if compacted > 0 {
		if err := os.Rename(tmp, path); err != nil {
			return 0, err
		}
	} else {
		_ = os.Remove(tmp)
	}
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("compacted=%d\n", compacted)), 0600); err != nil {
		return 0, err
	}
	ok = true
	return compacted, nil
}

func rewriteCheckpointObject(dec *json.Decoder, w *bufio.Writer) (int, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, errors.New("state checkpoint must be a JSON object")
	}
	if err := w.WriteByte('{'); err != nil {
		return 0, err
	}
	first := true
	compacted := 0
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return 0, err
		}
		key, ok := kt.(string)
		if !ok {
			return 0, errors.New("state checkpoint object key is not a string")
		}
		if !first {
			if err := w.WriteByte(','); err != nil {
				return 0, err
			}
		}
		first = false
		kb, _ := json.Marshal(key)
		if _, err := w.Write(kb); err != nil {
			return 0, err
		}
		if err := w.WriteByte(':'); err != nil {
			return 0, err
		}
		if key == "jobs" {
			n, err := rewriteJobsObject(dec, w)
			if err != nil {
				return 0, err
			}
			compacted += n
			continue
		}
		if err := copyJSONValue(dec, w); err != nil {
			return 0, err
		}
	}
	if _, err := dec.Token(); err != nil { // closing }
		return 0, err
	}
	if err := w.WriteByte('}'); err != nil {
		return 0, err
	}
	if tok, err := dec.Token(); err != io.EOF {
		if err == nil {
			return 0, fmt.Errorf("unexpected trailing JSON token %v", tok)
		}
		return 0, err
	}
	return compacted, nil
}

func rewriteJobsObject(dec *json.Decoder, w *bufio.Writer) (int, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	if tok == nil {
		_, err = w.WriteString("null")
		return 0, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, errors.New("jobs must be a JSON object")
	}
	if err := w.WriteByte('{'); err != nil {
		return 0, err
	}
	first := true
	compacted := 0
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return 0, err
		}
		key := kt.(string)
		var job core.Job
		if err := dec.Decode(&job); err != nil {
			return 0, err
		}
		if job.Type == "vector.relink" && job.Status == "done" {
			if len(job.Payload) > 0 || len(job.Result) > 0 {
				compacted++
			}
			job.Payload = nil
			job.Result = nil
		}
		if !first {
			if err := w.WriteByte(','); err != nil {
				return 0, err
			}
		}
		first = false
		kb, _ := json.Marshal(key)
		jb, err := json.Marshal(job)
		if err != nil {
			return 0, err
		}
		if _, err := w.Write(kb); err != nil {
			return 0, err
		}
		if err := w.WriteByte(':'); err != nil {
			return 0, err
		}
		if _, err := w.Write(jb); err != nil {
			return 0, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return 0, err
	}
	return compacted, w.WriteByte('}')
}

func copyJSONValue(dec *json.Decoder, w *bufio.Writer) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok {
		switch d {
		case '{':
			if err := w.WriteByte('{'); err != nil {
				return err
			}
			first := true
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				if !first {
					if err := w.WriteByte(','); err != nil {
						return err
					}
				}
				first = false
				kb, _ := json.Marshal(kt.(string))
				if _, err := w.Write(kb); err != nil {
					return err
				}
				if err := w.WriteByte(':'); err != nil {
					return err
				}
				if err := copyJSONValue(dec, w); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			return w.WriteByte('}')
		case '[':
			if err := w.WriteByte('['); err != nil {
				return err
			}
			first := true
			for dec.More() {
				if !first {
					if err := w.WriteByte(','); err != nil {
						return err
					}
				}
				first = false
				if err := copyJSONValue(dec, w); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			return w.WriteByte(']')
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", d)
		}
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
