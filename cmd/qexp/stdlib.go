package main

import (
	"strings"
)

var (
	stdList []string
)

func init() {
	list := strings.Split(stdlib, "\n")
	for _, v := range list {
		if v == "" {
			continue
		}
		if strings.Contains(v, "internal/") || strings.Contains(v, "vendor/") {
			continue
		}
		// skip syscall
		if v == "syscall" {
			continue
		}
		stdList = append(stdList, v)
	}
}

var stdlib string = `
archive/tar
archive/zip
bufio
bytes
compress/bzip2
compress/flate
compress/gzip
compress/lzw
compress/zlib
container/heap
container/list
container/ring
context
crypto
crypto/aes
crypto/cipher
crypto/des
crypto/dsa
crypto/ecdsa
crypto/ed25519
crypto/ed25519/internal/edwards25519
crypto/ed25519/internal/edwards25519/field
crypto/elliptic
crypto/elliptic/internal/fiat
crypto/elliptic/internal/nistec
crypto/hmac
crypto/internal/randutil
crypto/internal/subtle
crypto/md5
crypto/rand
crypto/rc4
crypto/rsa
crypto/sha1
crypto/sha256
crypto/sha512
crypto/subtle
crypto/tls
crypto/x509
crypto/x509/internal/macos
crypto/x509/pkix
database/sql
database/sql/driver
debug/buildinfo
debug/dwarf
debug/elf
debug/gosym
debug/macho
debug/pe
debug/plan9obj
embed
embed/internal/embedtest
encoding
encoding/ascii85
encoding/asn1
encoding/base32
encoding/base64
encoding/binary
encoding/csv
encoding/gob
encoding/hex
encoding/json
encoding/pem
encoding/xml
errors
expvar
flag
fmt
go/ast
go/build
go/build/constraint
go/constant
go/doc
go/doc/comment
go/format
go/importer
go/internal/gccgoimporter
go/internal/gcimporter
go/internal/srcimporter
go/internal/typeparams
go/parser
go/printer
go/scanner
go/token
go/types
hash
hash/adler32
hash/crc32
hash/crc64
hash/fnv
hash/maphash
html
html/template
image
image/color
image/color/palette
image/draw
image/gif
image/internal/imageutil
image/jpeg
image/png
index/suffixarray
internal/abi
internal/buildcfg
internal/bytealg
internal/cfg
internal/cpu
internal/diff
internal/execabs
internal/fmtsort
internal/fuzz
internal/goarch
internal/godebug
internal/goexperiment
internal/goos
internal/goroot
internal/goversion
internal/intern
internal/itoa
internal/lazyregexp
internal/lazytemplate
internal/nettrace
internal/obscuretestdata
internal/oserror
internal/pkgbits
internal/poll
internal/profile
internal/race
internal/reflectlite
internal/singleflight
internal/syscall/execenv
internal/syscall/unix
internal/sysinfo
internal/testenv
internal/testlog
internal/trace
internal/txtar
internal/unsafeheader
internal/xcoff
io
io/fs
io/ioutil
log
log/syslog
math
math/big
math/bits
math/cmplx
math/rand
mime
mime/multipart
mime/quotedprintable
net
net/http
net/http/cgi
net/http/cookiejar
net/http/fcgi
net/http/httptest
net/http/httptrace
net/http/httputil
net/http/internal
net/http/internal/ascii
net/http/internal/testcert
net/http/pprof
net/internal/socktest
net/mail
net/netip
net/rpc
net/rpc/jsonrpc
net/smtp
net/textproto
net/url
os
os/exec
os/exec/internal/fdtest
os/signal
os/signal/internal/pty
os/user
path
path/filepath
plugin
reflect
reflect/internal/example1
reflect/internal/example2
regexp
regexp/syntax
runtime
runtime/cgo
runtime/debug
runtime/internal/atomic
runtime/internal/math
runtime/internal/sys
runtime/metrics
runtime/pprof
runtime/race
runtime/trace
sort
strconv
strings
sync
sync/atomic
syscall
testing
testing/fstest
testing/internal/testdeps
testing/iotest
testing/quick
text/scanner
text/tabwriter
text/template
text/template/parse
time
time/tzdata
unicode
unicode/utf16
unicode/utf8
unsafe
vendor/golang.org/x/crypto/chacha20
vendor/golang.org/x/crypto/chacha20poly1305
vendor/golang.org/x/crypto/cryptobyte
vendor/golang.org/x/crypto/cryptobyte/asn1
vendor/golang.org/x/crypto/curve25519
vendor/golang.org/x/crypto/curve25519/internal/field
vendor/golang.org/x/crypto/hkdf
vendor/golang.org/x/crypto/internal/poly1305
vendor/golang.org/x/crypto/internal/subtle
vendor/golang.org/x/net/dns/dnsmessage
vendor/golang.org/x/net/http/httpguts
vendor/golang.org/x/net/http/httpproxy
vendor/golang.org/x/net/http2/hpack
vendor/golang.org/x/net/idna
vendor/golang.org/x/net/nettest
vendor/golang.org/x/net/route
vendor/golang.org/x/sys/cpu
vendor/golang.org/x/text/secure/bidirule
vendor/golang.org/x/text/transform
vendor/golang.org/x/text/unicode/bidi
vendor/golang.org/x/text/unicode/norm
`
