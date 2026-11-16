"""
LibreTranslate API
"""
__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

import os
from typing import List, Optional

import requests

from deep_translator.base import BaseTranslator
from deep_translator.constants import (
    BASE_URLS,
    LIBRE_ENV_VAR,
    LIBRE_LANGUAGES_TO_CODES,
)
from deep_translator.exceptions import (
    ApiKeyException,
    AuthorizationException,
    ServerException,
    TranslationNotFound,
)
from deep_translator.validate import is_empty, is_input_valid, request_failed


class LibreTranslator(BaseTranslator):
    """
    class that wraps functions, which use libre translator under the hood to translate text(s)
    """

    def __init__(
        self,
        source: str = "en",
        target: str = "es",
        api_key: Optional[str] = os.getenv(LIBRE_ENV_VAR, None),
        use_free_api: bool = True,
        custom_url: Optional[str] = None,
        **kwargs
    ):
        """
        @param api_key: your api key
        @param source: source language to translate from
        List of LibreTranslate endpoint can be found at :
        https://github.com/LibreTranslate/LibreTranslate#mirrors
        Some require an API key
        @param target: target language to translate to
        @param use_free_api: set True if you want to use the free api.
        This means a url that does not require and api key would be used
        @param custom_url: you can use a custom endpoint
        """
        if not api_key:
            raise ApiKeyException(env_var=LIBRE_ENV_VAR)

        self.api_key = api_key
        url = (
            BASE_URLS.get("LIBRE")
            if not use_free_api
            else BASE_URLS.get("LIBRE_FREE")
        )
        super().__init__(
            base_url=url if not custom_url else custom_url,
            source=source,
            target=target,
            languages=LIBRE_LANGUAGES_TO_CODES,
        )

    def translate(self, text: str, **kwargs) -> str:
        """
        function that uses microsoft translate to translate a text
        @param text: desired text to translate
        @return: str: translated text
        """
        if is_input_valid(text):
            if self._same_source_target() or is_empty(text):
                return text

            translate_endpoint = "translate"
            params = {
                "q": text,
                "source": self._source,
                "target": self._target,
                "format": "text",
            }
            # Add API Key if required
            if self.api_key:
                params["api_key"] = self.api_key
            # Do the request and check the connection.
            try:
                response = requests.post(
                    self._base_url + translate_endpoint, params=params
                )
            except ConnectionError:
                raise ServerException(503)
            # If the answer is not success, raise server exception.

            if response.status_code == 403:
                raise AuthorizationException(self.api_key)
            elif request_failed(status_code=response.status_code):
                raise ServerException(response.status_code)
            # Get the response and check is not empty.
            res = response.json()
            if not res:
                raise TranslationNotFound(text)
            # Process and return the response.
            return res["translatedText"]

    def translate_file(self, path: str, **kwargs) -> str:
        """
        translate directly from file
        @param path: path to the target file
        @type path: str
        @param kwargs: additional args
        @return: str
        """
        return self._translate_file(path, **kwargs)

    def translate_batch(self, batch: List[str], **kwargs) -> List[str]:
        """
        translate a list of texts
        @param batch: list of texts you want to translate
        @return: list of translations
        """
        return self._translate_batch(batch, **kwargs)
