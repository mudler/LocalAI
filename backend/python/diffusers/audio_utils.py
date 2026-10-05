import array
import sys
import wave


def write_pcm_wav(destination, samples, sampling_rate):
    """Write normalized floating-point audio samples as mono 16-bit PCM."""
    pcm = array.array(
        "h",
        (
            max(-32768, min(32767, round(float(sample) * 32768)))
            for sample in samples
        ),
    )
    if pcm.itemsize != 2:
        raise RuntimeError("16-bit PCM requires two-byte signed integers")
    if sys.byteorder != "little":
        pcm.byteswap()

    with wave.open(destination, "wb") as output:
        output.setnchannels(1)
        output.setsampwidth(2)
        output.setframerate(sampling_rate)
        output.writeframes(pcm.tobytes())
