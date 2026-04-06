package queue

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"mailapi/internal/model"
)

var (
	errInvalidPayload = errors.New("invalid queue payload")
)

var emailMagic = [4]byte{'M', 'A', 'I', 'L'}

const (
	emailCodecVersion byte = 1

	maxToCount   = 2048
	maxStringLen = 16 << 10  // 16KB
	maxRawLen    = 64 << 20  // 64MB
)

func encodeIncomingEmail(email *model.IncomingEmail) ([]byte, error) {
	if email == nil {
		return nil, fmt.Errorf("%w: nil email", errInvalidPayload)
	}

	from := email.From
	remote := email.RemoteAddr
	to := email.To
	raw := email.RawMessage

	if len(from) > maxStringLen || len(remote) > maxStringLen {
		return nil, fmt.Errorf("%w: string too large", errInvalidPayload)
	}
	if len(to) > maxToCount {
		return nil, fmt.Errorf("%w: too many recipients", errInvalidPayload)
	}
	for _, r := range to {
		if len(r) > maxStringLen {
			return nil, fmt.Errorf("%w: recipient too large", errInvalidPayload)
		}
	}
	if len(raw) > maxRawLen {
		return nil, fmt.Errorf("%w: raw message too large", errInvalidPayload)
	}

	// 预估容量：避免多次扩容与拷贝。
	capHint := 5 + len(from) + len(remote) + len(raw)
	for _, r := range to {
		capHint += len(r)
	}
	capHint += 64 // varint 与计数开销粗估

	buf := make([]byte, 0, capHint)
	buf = append(buf, emailMagic[:]...)
	buf = append(buf, emailCodecVersion)

	buf = appendString(buf, from)
	buf = appendString(buf, remote)
	buf = appendUvarint(buf, uint64(email.ReceivedAt))

	buf = appendUvarint(buf, uint64(len(to)))
	for _, r := range to {
		buf = appendString(buf, r)
	}

	buf = appendBytes(buf, raw)
	return buf, nil
}

func decodeIncomingEmail(data []byte, out *model.IncomingEmail) error {
	if out == nil {
		return fmt.Errorf("%w: nil out", errInvalidPayload)
	}
	if len(data) == 0 {
		return fmt.Errorf("%w: empty", errInvalidPayload)
	}

	// 兼容旧版本：历史消息可能仍然是 JSON（RawMessage base64）。
	if len(data) < 5 || data[0] != emailMagic[0] || data[1] != emailMagic[1] || data[2] != emailMagic[2] || data[3] != emailMagic[3] {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%w: json fallback: %v", errInvalidPayload, err)
		}
		return nil
	}

	if data[4] != emailCodecVersion {
		return fmt.Errorf("%w: unsupported version %d", errInvalidPayload, data[4])
	}

	off := 5
	var err error

	out.From, err = readString(data, &off)
	if err != nil {
		return err
	}
	out.RemoteAddr, err = readString(data, &off)
	if err != nil {
		return err
	}

	receivedAt, err := readUvarint(data, &off)
	if err != nil {
		return err
	}
	out.ReceivedAt = int64(receivedAt)

	toCount, err := readUvarint(data, &off)
	if err != nil {
		return err
	}
	if toCount > maxToCount {
		return fmt.Errorf("%w: too many recipients", errInvalidPayload)
	}

	out.To = make([]string, 0, int(toCount))
	for range toCount {
		r, err := readString(data, &off)
		if err != nil {
			return err
		}
		out.To = append(out.To, r)
	}

	raw, err := readBytes(data, &off)
	if err != nil {
		return err
	}
	out.RawMessage = raw

	// 多余尾部视为错误，避免 silent corruption。
	if off != len(data) {
		return fmt.Errorf("%w: trailing bytes", errInvalidPayload)
	}
	return nil
}

func appendUvarint(dst []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(dst, tmp[:n]...)
}

func appendString(dst []byte, s string) []byte {
	dst = appendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

func appendBytes(dst []byte, b []byte) []byte {
	dst = appendUvarint(dst, uint64(len(b)))
	return append(dst, b...)
}

func readUvarint(data []byte, off *int) (uint64, error) {
	if off == nil {
		return 0, fmt.Errorf("%w: nil offset", errInvalidPayload)
	}
	i := *off
	if i < 0 || i >= len(data) {
		return 0, fmt.Errorf("%w: truncated", errInvalidPayload)
	}

	var x uint64
	var s uint
	for n := 0; n < binary.MaxVarintLen64; n++ {
		if i >= len(data) {
			return 0, fmt.Errorf("%w: truncated varint", errInvalidPayload)
		}
		b := data[i]
		i++
		if b < 0x80 {
			if n == binary.MaxVarintLen64-1 && b > 1 {
				return 0, fmt.Errorf("%w: varint overflow", errInvalidPayload)
			}
			*off = i
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, fmt.Errorf("%w: varint overflow", errInvalidPayload)
}

func readString(data []byte, off *int) (string, error) {
	n, err := readUvarint(data, off)
	if err != nil {
		return "", err
	}
	if n > maxStringLen {
		return "", fmt.Errorf("%w: string too large", errInvalidPayload)
	}
	i := *off
	if i < 0 || uint64(len(data)-i) < n {
		return "", fmt.Errorf("%w: truncated string", errInvalidPayload)
	}
	s := string(data[i : i+int(n)])
	*off = i + int(n)
	return s, nil
}

func readBytes(data []byte, off *int) ([]byte, error) {
	n, err := readUvarint(data, off)
	if err != nil {
		return nil, err
	}
	if n > maxRawLen {
		return nil, fmt.Errorf("%w: raw too large", errInvalidPayload)
	}
	i := *off
	if i < 0 || uint64(len(data)-i) < n {
		return nil, fmt.Errorf("%w: truncated bytes", errInvalidPayload)
	}
	b := data[i : i+int(n)]
	*off = i + int(n)
	return b, nil
}

