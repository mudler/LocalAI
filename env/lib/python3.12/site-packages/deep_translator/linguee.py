"""
linguee translator API
"""

__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

from typing import List, Optional, Union

import requests
from bs4 import BeautifulSoup
from requests.utils import requote_uri

from deep_translator.base import BaseTranslator
from deep_translator.constants import BASE_URLS, LINGUEE_LANGUAGES_TO_CODES
from deep_translator.exceptions import (
    ElementNotFoundInGetRequest,
    NotValidPayload,
    RequestError,
    TooManyRequests,
    TranslationNotFound,
)
from deep_translator.validate import is_empty, is_input_valid, request_failed


class LingueeTranslator(BaseTranslator):
    """
    class that wraps functions, which use the linguee translator under the hood to translate word(s)
    """

    def __init__(
        self,
        source: str = "en",
        target: str = "de",
        proxies: Optional[dict] = None,
        **kwargs,
    ):
        """
        @param source: source language to translate from
        @param target: target language to translate to
        """
        self.proxies = proxies
        super().__init__(
            base_url=BASE_URLS.get("LINGUEE"),
            source=source,
            target=target,
            languages=LINGUEE_LANGUAGES_TO_CODES,
            element_tag="a",
            element_query={"class": "dictLink featured"},
            payload_key=None,  # key of text in the url
        )

    def translate(
        self, word: str, return_all: bool = False, **kwargs
    ) -> Union[str, List[str]]:
        """
        function that uses linguee to translate a word
        @param word: word to translate
        @type word: str
        @param return_all: set to True to return all synonym of the translated word
        @type return_all: bool
        @return: str: translated word
        """
        if self._same_source_target() or is_empty(word):
            return word

        if is_input_valid(word, max_chars=50):
            # %s-%s/translation/%s.html
            url = f"{self._base_url}{self._source}-{self._target}/search/?source={self._source}&query={word}"
            url = requote_uri(url)
            response = requests.get(url, proxies=self.proxies)

            if response.status_code == 429:
                raise TooManyRequests()

            if request_failed(status_code=response.status_code):
                raise RequestError()

            soup = BeautifulSoup(response.text, "html.parser")
            elements = soup.find_all(self._element_tag, self._element_query)
            response.close()

            if not elements:
                raise ElementNotFoundInGetRequest(elements)

            filtered_elements = []
            for el in elements:
                try:
                    pronoun = el.find(
                        "span", {"class": "placeholder"}
                    ).get_text(strip=True)
                except AttributeError:
                    pronoun = ""
                filtered_elements.append(
                    el.get_text(strip=True).replace(pronoun, "")
                )

            if not filtered_elements:
                raise TranslationNotFound(word)

            return filtered_elements if return_all else filtered_elements[0]

    def translate_words(self, words: List[str], **kwargs) -> List[str]:
        """
        translate a batch of words together by providing them in a list
        @param words: list of words you want to translate
        @param kwargs: additional args
        @return: list of translated words
        """
        if not words:
            raise NotValidPayload(words)

        translated_words = []
        for word in words:
            translated_words.append(self.translate(word=word, **kwargs))
        return translated_words
