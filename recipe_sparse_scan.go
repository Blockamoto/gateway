package main

// A JSON grammar scanner that records byte spans without allocating strings or
// values it skips. It is used only on an owned, content-verified unit copy.
import (
	"bufio"
	"context"
	"fmt"
	"io"
)

type recipeJSONSpan struct{ Offset, Bytes int64 }
type recipeJSONScanner struct {
	ctx    context.Context
	r      *bufio.Reader
	offset int64
}

func newRecipeJSONScanner(ctx context.Context, r io.Reader) *recipeJSONScanner {
	return &recipeJSONScanner{ctx: ctx, r: bufio.NewReaderSize(r, 64<<10)}
}
func (s *recipeJSONScanner) peek() (byte, error) {
	if s.offset&((64<<10)-1) == 0 {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
	}
	b, err := s.r.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}
func (s *recipeJSONScanner) next() (byte, error) {
	b, err := s.peek()
	if err != nil {
		return 0, err
	}
	_, err = s.r.ReadByte()
	if err == nil {
		s.offset++
	}
	return b, err
}
func (s *recipeJSONScanner) space() error {
	for {
		b, err := s.peek()
		if err != nil {
			return err
		}
		if b != ' ' && b != '\n' && b != '\r' && b != '\t' {
			return nil
		}
		_, _ = s.next()
	}
}
func (s *recipeJSONScanner) expect(want byte) error {
	if err := s.space(); err != nil {
		return err
	}
	b, err := s.next()
	if err != nil {
		return err
	}
	if b != want {
		return fmt.Errorf("invalid sparse JSON at byte %d: expected %q", s.offset-1, want)
	}
	return nil
}
func (s *recipeJSONScanner) stringSpan() (recipeJSONSpan, error) {
	if err := s.space(); err != nil {
		return recipeJSONSpan{}, err
	}
	start := s.offset
	b, err := s.next()
	if err != nil || b != '"' {
		return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON string at byte %d", start)
	}
	for {
		b, err = s.next()
		if err != nil {
			return recipeJSONSpan{}, err
		}
		if b == '"' {
			return recipeJSONSpan{start, s.offset - start}, nil
		}
		if b < 0x20 {
			return recipeJSONSpan{}, fmt.Errorf("control byte in sparse JSON string")
		}
		if b == '\\' {
			b, err = s.next()
			if err != nil {
				return recipeJSONSpan{}, err
			}
			switch b {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				for i := 0; i < 4; i++ {
					b, err = s.next()
					if err != nil {
						return recipeJSONSpan{}, err
					}
					if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F') {
						return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON unicode escape")
					}
				}
			default:
				return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON escape")
			}
		}
	}
}
func (s *recipeJSONScanner) skip(depth int) (recipeJSONSpan, error) {
	if depth > 10000 {
		return recipeJSONSpan{}, fmt.Errorf("sparse JSON nesting exceeds JSON grammar limit")
	}
	if err := s.space(); err != nil {
		return recipeJSONSpan{}, err
	}
	start := s.offset
	b, err := s.peek()
	if err != nil {
		return recipeJSONSpan{}, err
	}
	switch b {
	case '"':
		return s.stringSpan()
	case '{', '[':
		if depth >= 10000 {
			return recipeJSONSpan{}, fmt.Errorf("sparse JSON nesting exceeds JSON grammar limit")
		}
		_, _ = s.next()
		end := byte(']')
		if b == '{' {
			end = '}'
		}
		if err = s.space(); err != nil {
			return recipeJSONSpan{}, err
		}
		next, _ := s.peek()
		if next == end {
			_, _ = s.next()
			break
		}
		for {
			if b == '{' {
				if _, err = s.stringSpan(); err != nil {
					return recipeJSONSpan{}, err
				}
				if err = s.expect(':'); err != nil {
					return recipeJSONSpan{}, err
				}
			}
			if _, err = s.skip(depth + 1); err != nil {
				return recipeJSONSpan{}, err
			}
			if err = s.space(); err != nil {
				return recipeJSONSpan{}, err
			}
			next, err = s.next()
			if err != nil {
				return recipeJSONSpan{}, err
			}
			if next == end {
				break
			}
			if next != ',' {
				return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON separator")
			}
		}
	case 't', 'f', 'n':
		literal := "null"
		if b == 't' {
			literal = "true"
		}
		if b == 'f' {
			literal = "false"
		}
		for i := range literal {
			got, e := s.next()
			if e != nil {
				return recipeJSONSpan{}, e
			}
			if got != literal[i] {
				return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON literal")
			}
		}
	default:
		if b == '-' {
			_, _ = s.next()
			b, err = s.peek()
			if err != nil {
				return recipeJSONSpan{}, err
			}
		}
		if b == '0' {
			_, _ = s.next()
		} else if b >= '1' && b <= '9' {
			for {
				_, _ = s.next()
				b, err = s.peek()
				if err == io.EOF {
					break
				}
				if err != nil {
					return recipeJSONSpan{}, err
				}
				if b < '0' || b > '9' {
					break
				}
			}
		} else {
			return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON number at byte %d", start)
		}
		b, err = s.peek()
		if err == io.EOF {
			return recipeJSONSpan{start, s.offset - start}, nil
		}
		if err != nil {
			return recipeJSONSpan{}, err
		}
		if b == '.' {
			_, _ = s.next()
			digits := 0
			for {
				b, err = s.peek()
				if err == io.EOF {
					break
				}
				if err != nil {
					return recipeJSONSpan{}, err
				}
				if b < '0' || b > '9' {
					break
				}
				_, _ = s.next()
				digits++
			}
			if digits == 0 {
				return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON fraction")
			}
		}
		b, err = s.peek()
		if err == io.EOF {
			return recipeJSONSpan{start, s.offset - start}, nil
		}
		if err != nil {
			return recipeJSONSpan{}, err
		}
		if b == 'e' || b == 'E' {
			_, _ = s.next()
			b, err = s.peek()
			if err != nil {
				return recipeJSONSpan{}, err
			}
			if b == '+' || b == '-' {
				_, _ = s.next()
			}
			digits := 0
			for {
				b, err = s.peek()
				if err == io.EOF {
					break
				}
				if err != nil {
					return recipeJSONSpan{}, err
				}
				if b < '0' || b > '9' {
					break
				}
				_, _ = s.next()
				digits++
			}
			if digits == 0 {
				return recipeJSONSpan{}, fmt.Errorf("invalid sparse JSON exponent")
			}
		}
	}
	return recipeJSONSpan{start, s.offset - start}, s.ctx.Err()
}
