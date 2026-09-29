package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
)

const targetTimespan = int64(14 * 24 * 60 * 60)

var (
	genesisHashDisplay = "000000000019d6689c085ae165831e934ff763ae46a2a6c172b3f1b60a8ce26f"
	genesisHeaderHex   = "01000000" +
		"0000000000000000000000000000000000000000000000000000000000000000" +
		"3ba3edfd7a7b12b27ac72c3e67768f617fc81bc3888a51323a9fb8aa4b1e5e4a" +
		"29ab5f49ffff001d1dac2b7c"
)

func ensureHeaderFile(path string) (int64, error) {
	if err := recoverHeaderAppend051(path); err != nil {
		return 0, err
	}
	genesis, _ := hex.DecodeString(genesisHeaderHex)
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, genesis, 0644); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	size := st.Size()
	if size < 80 {
		if err := os.WriteFile(path, genesis, 0644); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if rem := size % 80; rem != 0 {
		if err := os.Truncate(path, size-rem); err != nil {
			return 0, err
		}
		size -= rem
	}
	first, err := readHeaderAt(path, 0)
	if err != nil {
		return 0, err
	}
	got := hash256(first)
	if reverseHex(got[:]) != genesisHashDisplay {
		return 0, fmt.Errorf("header cache does not begin with Bitcoin mainnet genesis")
	}
	return size / 80, nil
}

func syncHeaders(path string, preferred []string, progress func(int64, string, int64)) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	count := st.Size() / 80
	attempts := 0
	for attempts < 12 {
		last, err := readHeaderAt(path, count-1)
		if err != nil {
			return count, err
		}
		lastHash := hash256(last)
		p, err := connectAnyPeer(preferred, false)
		if err != nil {
			attempts++
			continue
		}
		if progress != nil {
			progress(count, p.addr, p.startHeight)
		}
		batchErr := func() error {
			defer p.conn.Close()
			for {
				req := makeGetHeaders(lastHash)
				if err := writeMessage(p.conn, "getheaders", req); err != nil {
					return err
				}
				msg, err := waitForCommand(p, "headers", 25_000_000_000)
				if err != nil {
					return err
				}
				headers, err := parseHeadersPayload(msg.payload)
				if err != nil {
					return err
				}
				if len(headers) == 0 {
					return nil
				}
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					return err
				}
				for _, h := range headers {
					prev := h[4:36]
					if !bytes.Equal(prev, lastHash[:]) {
						f.Close()
						return fmt.Errorf("peer sent a header that does not connect to our tip")
					}
					if err := verifyHeaderConsensusBasics(path, count, h); err != nil {
						f.Close()
						return err
					}
					if _, err := f.Write(h); err != nil {
						f.Close()
						return err
					}
					lastHash = hash256(h)
					count++
				}
				if err := f.Close(); err != nil {
					return err
				}
				if progress != nil {
					progress(count, p.addr, p.startHeight)
				}
			}
		}()
		if batchErr == nil {
			return count, nil
		}
		attempts++
	}
	return count, fmt.Errorf("could not complete header sync after multiple peers")
}

func makeGetHeaders(locator [32]byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, protocolVersion)
	b.Write(encodeVarInt(1))
	b.Write(locator[:])
	b.Write(make([]byte, 32))
	return b.Bytes()
}

func parseHeadersPayload(p []byte) ([][]byte, error) {
	count, n, err := decodeCompactSizeMinimal(p)
	if err != nil {
		return nil, err
	}
	if count > headersPerMsg {
		return nil, fmt.Errorf("peer sent %d headers", count)
	}
	off := n
	out := make([][]byte, 0, count)
	for i := uint64(0); i < count; i++ {
		if off+80 > len(p) {
			return nil, io.ErrUnexpectedEOF
		}
		h := append([]byte(nil), p[off:off+80]...)
		off += 80
		txc, n2, err := decodeCompactSizeMinimal(p[off:])
		if err != nil {
			return nil, err
		}
		if txc != 0 {
			return nil, fmt.Errorf("headers entry had nonzero tx count")
		}
		off += n2
		out = append(out, h)
	}
	if off != len(p) {
		return nil, fmt.Errorf("trailing bytes in headers payload")
	}
	return out, nil
}

func readHeaderAt(path string, height int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(height*80, io.SeekStart); err != nil {
		return nil, err
	}
	h := make([]byte, 80)
	_, err = io.ReadFull(f, h)
	return h, err
}

func findHeaderHeightByHash(path, wanted string) (int64, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1, nil, err
	}
	for off, height := 0, int64(0); off+80 <= len(data); off, height = off+80, height+1 {
		h := data[off : off+80]
		hash := hash256(h)
		if reverseHex(hash[:]) == wanted {
			return height, append([]byte(nil), h...), nil
		}
	}
	return -1, nil, fmt.Errorf("block hash was not found in the synced main-chain headers")
}

func verifyHeaderConsensusBasics(path string, height int64, h []byte) error {
	return verifyHeaderConsensusWithReader(func(n int64) ([]byte, error) { return readHeaderAt(path, n) }, height, h)
}
func verifyHeaderConsensusWithReader(read func(int64) ([]byte, error), height int64, h []byte) error {
	if len(h) != 80 {
		return fmt.Errorf("header length %d", len(h))
	}
	if err := verifyPoW(h); err != nil {
		return err
	}
	if height == 0 {
		return nil
	}
	prev, err := read(height - 1)
	if err != nil {
		return err
	}
	bits := binary.LittleEndian.Uint32(h[72:76])
	prevBits := binary.LittleEndian.Uint32(prev[72:76])
	if height%2016 != 0 {
		if bits != prevBits {
			return fmt.Errorf("unexpected difficulty bits at height %d: got %08x expected %08x", height, bits, prevBits)
		}
		return nil
	}
	first, err := read(height - 2016)
	if err != nil {
		return err
	}
	firstTime := int64(binary.LittleEndian.Uint32(first[68:72]))
	lastTime := int64(binary.LittleEndian.Uint32(prev[68:72]))
	actual := lastTime - firstTime
	if actual < targetTimespan/4 {
		actual = targetTimespan / 4
	}
	if actual > targetTimespan*4 {
		actual = targetTimespan * 4
	}
	target := compactToBig(prevBits)
	target.Mul(target, big.NewInt(actual))
	target.Div(target, big.NewInt(targetTimespan))
	powLimit := compactToBig(0x1d00ffff)
	if target.Cmp(powLimit) > 0 {
		target = powLimit
	}
	expected := bigToCompact(target)
	if bits != expected {
		return fmt.Errorf("unexpected retarget at height %d: got %08x expected %08x", height, bits, expected)
	}
	return nil
}

func verifyPoW(h []byte) error {
	if len(h) != 80 {
		return fmt.Errorf("header length %d", len(h))
	}
	bits := binary.LittleEndian.Uint32(h[72:76])
	target := compactToBig(bits)
	if target.Sign() <= 0 {
		return fmt.Errorf("invalid target")
	}
	powLimit := compactToBig(0x1d00ffff)
	if target.Cmp(powLimit) > 0 {
		return fmt.Errorf("target exceeds mainnet pow limit")
	}
	digest := hash256(h)
	rev := make([]byte, 32)
	for i := 0; i < 32; i++ {
		rev[i] = digest[31-i]
	}
	hashNum := new(big.Int).SetBytes(rev)
	if hashNum.Cmp(target) > 0 {
		return fmt.Errorf("invalid proof of work")
	}
	return nil
}

func compactToBig(compact uint32) *big.Int {
	size := compact >> 24
	word := compact & 0x007fffff
	n := new(big.Int).SetUint64(uint64(word))
	if size <= 3 {
		n.Rsh(n, uint(8*(3-size)))
	} else {
		n.Lsh(n, uint(8*(size-3)))
	}
	if compact&0x00800000 != 0 {
		n.Neg(n)
	}
	return n
}

func bigToCompact(n *big.Int) uint32 {
	if n.Sign() == 0 {
		return 0
	}
	negative := n.Sign() < 0
	t := new(big.Int).Abs(new(big.Int).Set(n))
	size := uint32((t.BitLen() + 7) / 8)
	var compact uint32
	if size <= 3 {
		compact = uint32(t.Uint64() << (8 * (3 - size)))
	} else {
		shift := uint(8 * (size - 3))
		tmp := new(big.Int).Rsh(t, shift)
		compact = uint32(tmp.Uint64())
	}
	if compact&0x00800000 != 0 {
		compact >>= 8
		size++
	}
	compact |= size << 24
	if negative && compact&0x007fffff != 0 {
		compact |= 0x00800000
	}
	return compact
}
