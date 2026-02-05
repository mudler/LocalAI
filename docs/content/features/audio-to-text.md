+++
disableToc = false
title = "🔈 Audio to text"
weight = 16
url = "/features/audio-to-text/"
+++

Audio to text models are models that can generate text from an audio file.

The transcription endpoint allows to convert audio files to text. The endpoint supports multiple backends:
- **[whisper.cpp](https://github.com/ggerganov/whisper.cpp)**: A C++ library for audio transcription (default)
- **moonshine**: Ultra-fast transcription engine optimized for low-end devices
- **faster-whisper**: Fast Whisper implementation with CTranslate2

The endpoint input supports all the audio formats supported by `ffmpeg`.

## Usage

Once LocalAI is started and whisper models are installed, you can use the `/v1/audio/transcriptions` API endpoint.

For instance, with cURL:

```bash
curl http://localhost:8080/v1/audio/transcriptions -H "Content-Type: multipart/form-data" -F file="@<FILE_PATH>" -F model="<MODEL_NAME>"
```

## Example

Download one of the models from [here](https://huggingface.co/ggerganov/whisper.cpp/tree/main) in the `models` folder,
and create a YAML file for your model:

```yaml
name: whisper-1
backend: whisper
parameters:
  model: whisper-en
```

The transcriptions endpoint then can be tested like so:

```bash
## Get an example audio file
wget --quiet --show-progress -O gb1.ogg https://upload.wikimedia.org/wikipedia/commons/1/1f/George_W_Bush_Columbia_FINAL.ogg

## Send the example audio file to the transcriptions endpoint
curl http://localhost:8080/v1/audio/transcriptions -H "Content-Type: multipart/form-data" -F file="@$PWD/gb1.ogg" -F model="whisper-1"
```

Result:

```json
{
  "segments":[{"id":0,"start":0,"end":9640000000,"text":" My fellow Americans, this day has brought terrible news and great sadness to our country.","tokens":[50364,1222,7177,6280,11,341,786,575,3038,6237,2583,293,869,22462,281,527,1941,13,50846]},{"id":1,"start":9640000000,"end":15960000000,"text":" At 9 o'clock this morning, Mission Control and Houston lost contact with our Space Shuttle","tokens":[1711,1722,277,6,9023,341,2446,11,20170,12912,293,18717,2731,3385,365,527,8705,13870,10972,51162]},{"id":2,"start":15960000000,"end":16960000000,"text":" Columbia.","tokens":[17339,13,51212]},{"id":3,"start":16960000000,"end":24640000000,"text":" A short time later, debris was seen falling from the skies above Texas.","tokens":[316,2099,565,1780,11,21942,390,1612,7440,490,264,25861,3673,7885,13,51596]},{"id":4,"start":24640000000,"end":27200000000,"text":" The Columbia's lost.","tokens":[440,17339,311,2731,13,51724]},{"id":5,"start":27200000000,"end":29920000000,"text":" There are no survivors.","tokens":[821,366,572,18369,13,51860]},{"id":6,"start":29920000000,"end":32920000000,"text":" And board was a crew of seven.","tokens":[50364,400,3150,390,257,7260,295,3407,13,50514]},{"id":7,"start":32920000000,"end":39780000000,"text":" Colonel Rick Husband, Lieutenant Colonel Michael Anderson, Commander Laurel Clark, Captain","tokens":[28478,11224,21282,4235,11,28412,28478,5116,18768,11,20857,27270,75,18572,11,10873,50857]},{"id":8,"start":39780000000,"end":50020000000,"text":" David Brown, Commander William McCool, Dr. Cooltna Chavla, and Elon Ramon, a Colonel","tokens":[4389,8030,11,20857,6740,4050,34,1092,11,2491,13,8561,83,629,761,706,875,11,293,28498,9078,266,11,257,28478,51369]},{"id":9,"start":50020000000,"end":52800000000,"text":" in the Israeli Air Force.","tokens":[294,264,19974,5774,10580,13,51508]},{"id":10,"start":52800000000,"end":58480000000,"text":" These men and women assumed great risk in the service to all humanity.","tokens":[1981,1706,293,2266,15895,869,3148,294,264,2643,281,439,10243,13,51792]},{"id":11,"start":58480000000,"end":63120000000,"text":" And an age when Space Flight has come to seem almost routine.","tokens":[50364,400,364,3205,562,8705,28954,575,808,281,1643,1920,9927,13,50596]},{"id":12,"start":63120000000,"end":68800000000,"text":" It is easy to overlook the dangers of travel by rocket and the difficulties of navigating","tokens":[467,307,1858,281,37826,264,27701,295,3147,538,13012,293,264,14399,295,32054,50880]},{"id":13,"start":68800000000,"end":72640000000,"text":" the fierce outer atmosphere of the Earth.","tokens":[264,25341,10847,8018,295,264,4755,13,51072]},{"id":14,"start":72640000000,"end":78040000000,"text":" These astronauts knew the dangers and they faced them willingly.","tokens":[1981,28273,2586,264,27701,293,436,11446,552,44675,13,51342]},{"id":15,"start":78040000000,"end":83040000000,"text":" Knowing they had a high and noble purpose in life.","tokens":[25499,436,632,257,1090,293,20171,4334,294,993,13,51592]},{"id":16,"start":83040000000,"end":90800000000,"text":" Because of their courage and daring and idealism, we will miss them all the more.","tokens":[50364,1436,295,641,9892,293,43128,293,7157,1434,11,321,486,1713,552,439,264,544,13,50752]},{"id":17,"start":90800000000,"end":96560000000,"text":" All Americans today are thinking as well of the families of these men and women who have","tokens":[1057,6280,965,366,1953,382,731,295,264,4466,295,613,1706,293,2266,567,362,51040]},{"id":18,"start":96560000000,"end":100440000000,"text":" been given this sudden shock in grief.","tokens":[668,2212,341,3990,5588,294,18998,13,51234]},{"id":19,"start":100440000000,"end":102400000000,"text":" You're not alone.","tokens":[509,434,406,3312,13,51332]},{"id":20,"start":102400000000,"end":105440000000,"text":" Our entire nation agrees with you.","tokens":[2621,2302,4790,26383,365,291,13,51484]},{"id":21,"start":105440000000,"end":112360000000,"text":" And those you loved will always have the respect and gratitude of this country.","tokens":[400,729,291,4333,486,1009,362,264,3104,293,16935,295,341,1941,13,51830]},{"id":22,"start":112360000000,"end":116600000000,"text":" The cause in which they died will continue.","tokens":[50364,440,3082,294,597,436,4539,486,2354,13,50576]},{"id":23,"start":116600000000,"end":124240000000,"text":" Man kind is led into the darkness beyond our world by the inspiration of discovery and the","tokens":[2458,733,307,4684,666,264,11262,4399,527,1002,538,264,10249,295,12114,293,264,50958]},{"id":24,"start":124240000000,"end":127000000000,"text":" longing to understand.","tokens":[35050,281,1223,13,51096]},{"id":25,"start":127000000000,"end":131160000000,"text":" Our journey into space will go on.","tokens":[2621,4671,666,1901,486,352,322,13,51304]},{"id":26,"start":131160000000,"end":136480000000,"text":" In the skies today, we saw destruction and tragedy.","tokens":[682,264,25861,965,11,321,1866,13563,293,18563,13,51570]},{"id":27,"start":136480000000,"end":142080000000,"text":" As farther than we can see, there is comfort and hope.","tokens":[1018,20344,813,321,393,536,11,456,307,3400,293,1454,13,51850]},{"id":28,"start":142080000000,"end":149800000000,"text":" In the words of the prophet Isaiah, lift your eyes and look to the heavens who created","tokens":[50364,682,264,2283,295,264,18566,27263,11,5533,428,2575,293,574,281,264,26011,567,2942,50750]},{"id":29,"start":149800000000,"end":151640000000,"text":" all these.","tokens":[439,613,13,50842]},{"id":30,"start":151640000000,"end":159960000000,"text":" He who brings out the story hosts one by one and calls them each by name because of his great","tokens":[634,567,5607,484,264,1657,21573,472,538,472,293,5498,552,1184,538,1315,570,295,702,869,51258]},{"id":31,"start":159960000000,"end":163400000000,"text":" power and mighty strength.","tokens":[1347,293,21556,3800,13,51430]},{"id":32,"start":163400000000,"end":166400000000,"text":" Not one of them is missing.","tokens":[1726,472,295,552,307,5361,13,51580]},{"id":33,"start":166400000000,"end":173600000000,"text":" The same creator who names the stars also knows the names of the seven souls we mourn","tokens":[50364,440,912,14181,567,5288,264,6105,611,3255,264,5288,295,264,3407,16588,321,22235,77,50724]},{"id":34,"start":173600000000,"end":175600000000,"text":" today.","tokens":[965,13,50824]},{"id":35,"start":175600000000,"end":183160000000,"text":" The crew of the shuttle Columbia did not return safely to earth yet we can pray that all","tokens":[440,7260,295,264,26728,17339,630,406,2736,11750,281,4120,1939,321,393,3690,300,439,51202]},{"id":36,"start":183160000000,"end":185840000000,"text":" are safely home.","tokens":[366,11750,1280,13,51336]},{"id":37,"start":185840000000,"end":192600000000,"text":" May God bless the grieving families and may God continue to bless America.","tokens":[1891,1265,5227,264,48454,4466,293,815,1265,2354,281,5227,3374,13,51674]},{"id":38,"start":196400000000,"end":206400000000,"text":" [BLANK_AUDIO]","tokens":[50364,542,37592,62,29937,60,50864]}],
  "text":"My fellow Americans, this day has brought terrible news and great sadness to our country. At 9 o'clock this morning, Mission Control and Houston lost contact with our Space Shuttle Columbia. A short time later, debris was seen falling from the skies above Texas. The Columbia's lost. There are no survivors. And board was a crew of seven. Colonel Rick Husband, Lieutenant Colonel Michael Anderson, Commander Laurel Clark, Captain David Brown, Commander William McCool, Dr. Cooltna Chavla, and Elon Ramon, a Colonel in the Israeli Air Force. These men and women assumed great risk in the service to all humanity. And an age when Space Flight has come to seem almost routine. It is easy to overlook the dangers of travel by rocket and the difficulties of navigating the fierce outer atmosphere of the Earth. These astronauts knew the dangers and they faced them willingly. Knowing they had a high and noble purpose in life. Because of their courage and daring and idealism, we will miss them all the more. All Americans today are thinking as well of the families of these men and women who have been given this sudden shock in grief. You're not alone. Our entire nation agrees with you. And those you loved will always have the respect and gratitude of this country. The cause in which they died will continue. Man kind is led into the darkness beyond our world by the inspiration of discovery and the longing to understand. Our journey into space will go on. In the skies today, we saw destruction and tragedy. As farther than we can see, there is comfort and hope. In the words of the prophet Isaiah, lift your eyes and look to the heavens who created all these. He who brings out the story hosts one by one and calls them each by name because of his great power and mighty strength. Not one of them is missing. The same creator who names the stars also knows the names of the seven souls we mourn today. The crew of the shuttle Columbia did not return safely to earth yet we can pray that all are safely home. May God bless the grieving families and may God continue to bless America. [BLANK_AUDIO]"
}
```

---

You can also specify the `response_format` parameter to be one of `lrc`, `srt`, `vtt`, `text`, `json` or `json_verbose` (default):
```bash
## Send the example audio file to the transcriptions endpoint
curl http://localhost:8080/v1/audio/transcriptions -H "Content-Type: multipart/form-data" -F file="@$PWD/gb1.ogg" -F model="whisper-1" -F response_format="srt"
```

Result (first few lines):
```text
1
00:00:00,000 --> 00:00:09,640
My fellow Americans, this day has brought terrible news and great sadness to our country.

2
00:00:09,640 --> 00:00:15,960
At 9 o'clock this morning, Mission Control and Houston lost contact with our Space Shuttle

3
00:00:15,960 --> 00:00:16,960
Columbia.

4
00:00:16,960 --> 00:00:24,640
A short time later, debris was seen falling from the skies above Texas.

5
00:00:24,640 --> 00:00:27,200
The Columbia's lost.

6
00:00:27,200 --> 00:00:29,920
There are no survivors.
```
