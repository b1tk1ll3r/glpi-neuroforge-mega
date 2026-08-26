//go:build !linux

package store

func mapSegmentFile(path string) ([]byte, bool, error) { return nil, false, nil }
func unmapSegmentFile(b []byte) error                  { return nil }
