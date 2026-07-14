#include <opus.h>

int opus_shim_encoder_set_bitrate(OpusEncoder *st, opus_int32 bitrate) {
  return opus_encoder_ctl(st, OPUS_SET_BITRATE(bitrate));
}

int opus_shim_encoder_set_complexity(OpusEncoder *st, opus_int32 complexity) {
  return opus_encoder_ctl(st, OPUS_SET_COMPLEXITY(complexity));
}
