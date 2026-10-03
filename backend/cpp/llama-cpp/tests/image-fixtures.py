# SPDX-License-Identifier: MIT
"""Generate small compressed fixtures, including hostile IHDR/IDAT combinations."""
import base64
import json
import struct
import sys
import zlib

def png(w, h, pixels):
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(pixels)) + chunk(b'IEND', b'')

def url(raw):
    return 'data:image/png;base64,' + base64.b64encode(raw).decode()

fixtures = {
    'aggregate': url(png(3000, 3000, (b'\0' * 9001)*3000)),
    'dimension': url(png(4097, 1, b'\0'*12292)),
    'pixels': url(png(4096, 4096, b'\0')),
    'bomb': url(png(1, 1, b'\0'*1000000)),
    'truncated': url(png(1, 1, b'\0'*4)[:-15]),
    'red': url(png(64, 64, (b'\0'+b'\xff\0\0'*64)*64)),
    'blue': url(png(64, 64, (b'\0'+b'\0\0\xff'*64)*64)),
}
bad = bytearray(png(1, 1, b'\0'*4))
bad[29] ^= 1
fixtures['bad_crc'] = url(bad)
json.dump(fixtures, open(sys.argv[1], 'w'))
