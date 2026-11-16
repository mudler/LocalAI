"""
mymemory translator API
"""

__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

from typing import List, Optional, Union

import requests

from deep_translator.base import BaseTranslator
from deep_translator.constants import BASE_URLS, MY_MEMORY_LANGUAGES_TO_CODES
from deep_translator.exceptions import (
    RequestError,
    TooManyRequests,
    TranslationNotFound,
)
from deep_translator.validate import is_empty, is_input_valid, request_failed


class MyMemoryTranslator(BaseTranslator):
    """
    class that uses the mymemory translator to translate texts
    """

    def __init__(
        self,
        source: str = "auto",
        target: str = "en",
        proxies: Optional[dict] = None,
        **kwargs,
    ):
        """
        @param source: source language to translate from
        @param target: target language to translate to
        """
        self.proxies = proxies
        self.email = kwargs.get("email", None)
        super().__init__(
            base_url=BASE_URLS.get("MYMEMORY"),
            source=source,
            target=target,
            payload_key="q",
            languages=MY_MEMORY_LANGUAGES_TO_CODES,
        )

    def translate(
        self, text: str, return_all: bool = False, **kwargs
    ) -> Union[str, List[str]]:
        """
        function that uses the mymemory translator to translate a text
        @param text: desired text to translate
        @type text: str
        @param return_all: set to True to return all synonym/similars of the translated text
        @return: str or list
        """
        if is_input_valid(text, max_chars=500):
            text = text.strip()
            if self._same_source_target() or is_empty(text):
                return text

            self._url_params["langpair"] = f"{self._source}|{self._target}"
            if self.payload_key:
                self._url_params[self.payload_key] = text
            if self.email:
                self._url_params["de"] = self.email

            response = requests.get(
                self._base_url, params=self._url_params, proxies=self.proxies
            )

            if response.status_code == 429:
                raise TooManyRequests()
            if request_failed(status_code=response.status_code):
                raise RequestError()

            data = response.json()
            if not data:
                TranslationNotFound(text)

            response.close()
            translation = data.get("responseData").get("translatedText")
            all_matches = data.get("matches", [])

            if translation:
                if not return_all:
                    return translation
                else:
                    # append translation at the start of the matches list
                    return [translation] + list(all_matches)

            elif not translation:
                matches = (match["translation"] for match in all_matches)
                next_match = next(matches)
                return next_match if not return_all else list(all_matches)

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
