__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

import os
from typing import List, Optional

import requests

from deep_translator.base import BaseTranslator
from deep_translator.constants import (
    BASE_URLS,
    DEEPL_ENV_VAR,
    DEEPL_LANGUAGE_TO_CODE,
)
from deep_translator.exceptions import (
    ApiKeyException,
    AuthorizationException,
    ServerException,
    TranslationNotFound,
)
from deep_translator.validate import is_empty, is_input_valid, request_failed


class DeeplTranslator(BaseTranslator):
    """
    class that wraps functions, which use the DeeplTranslator translator
    under the hood to translate word(s)
    """

    def __init__(
        self,
        source: str = "de",
        target: str = "en",
        api_key: Optional[str] = os.getenv(DEEPL_ENV_VAR, None),
        use_free_api: bool = True,
        **kwargs
    ):
        """
        @param api_key: your DeeplTranslator api key.
        Get one here: https://www.deepl.com/docs-api/accessing-the-api/
        @param source: source language
        @param target: target language
        """
        if not api_key:
            raise ApiKeyException(env_var=DEEPL_ENV_VAR)

        self.version = "v2"
        self.api_key = api_key
        url = (
            BASE_URLS.get("DEEPL_FREE").format(version=self.version)
            if use_free_api
            else BASE_URLS.get("DEEPL").format(version=self.version)
        )
        super().__init__(
            base_url=url,
            source=source,
            target=target,
            languages=DEEPL_LANGUAGE_TO_CODE,
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
            translate_endpoint = "translate"
            params = {
                "auth_key": self.api_key,
                "source_lang": self._source,
                "target_lang": self._target,
                "text": text,
            }
            # Do the request and check the connection.
            try:
                response = requests.get(
                    self._base_url + translate_endpoint, params=params
                )
            except ConnectionError:
                raise ServerException(503)
            # If the answer is not success, raise server exception.
            if response.status_code == 403:
                raise AuthorizationException(self.api_key)
            if request_failed(status_code=response.status_code):
                raise ServerException(response.status_code)
            # Get the response and check is not empty.
            res = response.json()
            if not res:
                raise TranslationNotFound(text)
            # Process and return the response.
            return res["translations"][0]["text"]

    def translate_file(self, path: str, **kwargs) -> str:
        return self._translate_file(path, **kwargs)

    def translate_batch(self, batch: List[str], **kwargs) -> List[str]:
        """
        @param batch: list of texts to translate
        @return: list of translations
        """
        return self._translate_batch(batch, **kwargs)


if __name__ == "__main__":
    d = DeeplTranslator(target="en", api_key="some-key")
    t = d.translate("Ich habe keine ahnung")
    print("text: ", t)
