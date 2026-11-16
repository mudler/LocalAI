"""
language detection API
"""

__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

from typing import List, Optional, Union

import requests
from requests.exceptions import HTTPError

# Module global config
config = {
    "url": "https://ws.detectlanguage.com/0.2/detect",
    "headers": {
        "User-Agent": "Detect Language API Python Client 1.4.0",
        "Authorization": "Bearer {}",
    },
}


def get_request_body(
    text: Union[str, List[str]], api_key: str, *args, **kwargs
):
    """
    send a request and return the response body parsed as dictionary

    @param text: target text that you want to detect its language
    @type text: str
    @type api_key: str
    @param api_key: your private API key

    """
    if not api_key:
        raise Exception(
            "you need to get an API_KEY for this to work. "
            "Get one for free here: https://detectlanguage.com/documentation"
        )
    if not text:
        raise Exception("Please provide an input text")

    else:
        try:
            headers = config["headers"]
            headers["Authorization"] = headers["Authorization"].format(api_key)
            response = requests.post(
                config["url"], json={"q": text}, headers=headers
            )

            body = response.json().get("data")
            return body

        except HTTPError as e:
            print("Error occured while requesting from server: ", e.args)
            raise e


def single_detection(
    text: str,
    api_key: Optional[str] = None,
    detailed: bool = False,
    *args,
    **kwargs
):
    """
    function responsible for detecting the language from a text

    @param text: target text that you want to detect its language
    @type text: str
    @type api_key: str
    @param api_key: your private API key
    @param detailed: set to True if you want to get detailed
    information about the detection process
    """
    body = get_request_body(text, api_key)
    detections = body.get("detections")
    if detailed:
        return detections[0]

    lang = detections[0].get("language", None)
    if lang:
        return lang


def batch_detection(
    text_list: List[str], api_key: str, detailed: bool = False, *args, **kwargs
):
    """
    function responsible for detecting the language from a text

    @param text_list: target batch that you want to detect its language
    @param api_key: your private API key
    @param detailed: set to True if you want to
    get detailed information about the detection process
    """
    body = get_request_body(text_list, api_key)
    detections = body.get("detections")
    res = [obj[0] for obj in detections]
    if detailed:
        return res
    else:
        return [obj["language"] for obj in res]
