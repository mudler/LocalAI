"""
baidu translator API
"""

__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

import hashlib
import os
import random
from typing import List, Optional

import requests

from deep_translator.base import BaseTranslator
from deep_translator.constants import (
    BAIDU_APPID_ENV_VAR,
    BAIDU_APPKEY_ENV_VAR,
    BAIDU_LANGUAGE_TO_CODE,
    BASE_URLS,
)
from deep_translator.exceptions import (
    ApiKeyException,
    BaiduAPIerror,
    ServerException,
    TranslationNotFound,
)
from deep_translator.validate import is_empty, is_input_valid


class BaiduTranslator(BaseTranslator):
    """
    class that wraps functions, which use the BaiduTranslator translator
    under the hood to translate word(s)
    """

    def __init__(
        self,
        source: str = "en",
        target: str = "zh",
        appid: Optional[str] = os.getenv(BAIDU_APPID_ENV_VAR, None),
        appkey: Optional[str] = os.getenv(BAIDU_APPKEY_ENV_VAR, None),
        **kwargs
    ):
        """
        @param appid: your baidu cloud api appid.
        Get one here: https://fanyi-api.baidu.com/choose
        @param appkey: your baidu cloud api appkey.
        @param source: source language
        @param target: target language
        """
        if not appid:
            raise ApiKeyException(env_var=BAIDU_APPID_ENV_VAR)

        if not appkey:
            raise ApiKeyException(env_var=BAIDU_APPKEY_ENV_VAR)

        self.appid = appid
        self.appkey = appkey
        super().__init__(
            base_url=BASE_URLS.get("BAIDU"),
            source=source,
            target=target,
            languages=BAIDU_LANGUAGE_TO_CODE,
            **kwargs
        )

    def translate(self, text: str, **kwargs) -> str:
        """
        @param text: text to translate
        @return: translated text
        """
        if is_input_valid(text):
            if self._same_source_target() or is_empty(text):
                return text

            # Create the request parameters.
            salt = random.randint(32768, 65536)
            sign = hashlib.md5(
                (self.appid + text + str(salt) + self.appkey).encode("utf-8")
            ).hexdigest()
            headers = {"Content-Type": "application/x-www-form-urlencoded"}
            payload = {
                "appid": self.appid,
                "q": text,
                "from": self.source,
                "to": self.target,
                "salt": salt,
                "sign": sign,
            }

            # Do the request and check the connection.
            try:
                response = requests.post(
                    self._base_url, params=payload, headers=headers
                )
            except ConnectionError:
                raise ServerException(503)
            if response.status_code != 200:
                raise ServerException(response.status_code)
            # Get the response and check is not empty.
            res = response.json()
            if not res:
                raise TranslationNotFound(text)
            # Process and return the response.
            if "error_code" in res:
                raise BaiduAPIerror(res["error_msg"])
            if "trans_result" in res:
                return "\n".join([s["dst"] for s in res["trans_result"]])
            else:
                raise TranslationNotFound(text)

    def translate_file(self, path: str, **kwargs) -> str:
        return self._translate_file(path, **kwargs)

    def translate_batch(self, batch: List[str], **kwargs) -> List[str]:
        """
        @param batch: list of texts to translate
        @return: list of translations
        """
        return self._translate_batch(batch, **kwargs)
