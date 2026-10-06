# SPDX-License-Identifier: MIT
"""Generate small compressed fixtures, including hostile IHDR/IDAT combinations."""
import base64
import json
import io
from PIL import Image
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
# CRC-valid IDAT with an invalid zlib Adler-32 checksum.
bad = bytearray(base64.b64decode(fixtures['red'].split(',')[1]))
pos = bad.index(b'IDAT')
n = struct.unpack('>I', bad[pos-4:pos])[0]
bad[pos+4+n-1] ^= 1
bad[pos+4+n:pos+8+n] = struct.pack('>I', zlib.crc32(bad[pos:pos+4+n]))
try:
    zlib.decompress(bad[pos+4:pos+4+n])
    raise AssertionError('invalid Adler-32 accepted')
except zlib.error:
    pass
fixtures['bad_adler'] = url(bad)

def jpeg(w, h, progressive=False):
    out = io.BytesIO()
    Image.new('RGB', (w, h), 'red').save(out, format='JPEG', progressive=progressive)
    return out.getvalue()

def jpg_url(raw):
    return 'data:image/jpeg;base64,' + base64.b64encode(raw).decode()

jpg = jpeg(64, 64)
for key, raw in {
    'jpeg': jpg,
    'jpeg_progressive': jpeg(64, 64, True),
    # EOI inside a comment is data, not an end marker.
    'jpeg_embedded_marker': jpg[:2] + b'\xff\xfe\x00\x04\xff\xd9' + jpg[2:],
    'jpeg_missing_eoi': jpg[:-2],
    'jpeg_truncated_scan': jpg[:-30],
    'jpeg_appended_eoi': jpg[:-30] + b'\xff\xd9',
    'jpeg_embedded_missing_eoi': jpg[:2] + b'\xff\xfe\x00\x04\xff\xd9' + jpg[2:-2],
    'jpeg_dimension': jpeg(4097, 1),
    'jpeg_pixels': jpeg(4096, 4096),
}.items():
    fixtures[key] = jpg_url(raw)
json.dump(fixtures, open(sys.argv[1], 'w'))
