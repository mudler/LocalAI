OPUS_CFLAGS := $(shell pkg-config --cflags opus)
OPUS_LIBS := $(shell pkg-config --libs opus)

libopusshim.so: opus_shim.c
	$(CC) -shared -fPIC -o $@ $< $(OPUS_CFLAGS) $(OPUS_LIBS)

clean:
	rm -f libopusshim.so

.PHONY: clean
