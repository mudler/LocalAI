# -*- coding: utf-8 -*-

__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

import logging
import os
import sys
from typing import List, Optional

import requests

from deep_translator.base import BaseTranslator
from deep_translator.constants import BASE_URLS, MSFT_ENV_VAR
from deep_translator.exceptions import ApiKeyException, MicrosoftAPIerror
from deep_translator.validate import is_input_valid


class MicrosoftTranslator(BaseTranslator):
    """
    the class that wraps functions, which use the Microsoft translator under the hood to translate word(s)
    """

    def __init__(
        self,
        source: str = "auto",
        target: str = "en",
        api_key: Optional[str] = os.getenv(MSFT_ENV_VAR, None),
        region: Optional[str] = None,
        proxies: Optional[dict] = None,
        **kwargs,
    ):
        """
        @params api_key and target are the required params
        @param api_key: your Microsoft API key
        @param region: your Microsoft Location
        """

        if not api_key:
            raise ApiKeyException(env_var=MSFT_ENV_VAR)

        self.api_key = api_key
        self.proxies = proxies
        self.headers = {
            "Ocp-Apim-Subscription-Key": self.api_key,
            "Content-type": "application/json",
        }
        # parameter region is not required but very common and goes to headers if passed
        if region:
            self.region = region
            self.headers["Ocp-Apim-Subscription-Region"] = self.region
        super().__init__(
            base_url=BASE_URLS.get("MICROSOFT_TRANSLATE"),
            source=source,
            target=target,
            languages=self._get_supported_languages(),
            **kwargs,
        )

    # this function get the actual supported languages of the msft translator and store them in a dict, where
    # the keys are the abbreviations and the values are the languages
    # a common variable used in the other translators would be: MICROSOFT_CODES_TO_LANGUAGES
    def _get_supported_languages(self):
        microsoft_languages_api_url = (
            "https://api.cognitive.microsofttranslator.com/languages?api-version=3.0&scope"
            "=translation "
        )
        microsoft_languages_response = requests.get(
            microsoft_languages_api_url
        )
        translation_dict = microsoft_languages_response.json()["translation"]

        return {
            translation_dict[k]["name"].lower(): k.lower()
            for k in translation_dict.keys()
        }

    def translate(self, text: str, **kwargs) -> str:
        """
        function that uses microsoft translate to translate a text
        @param text: desired text to translate
        @return: str: translated text
        """
        # a body must be a list of dicts to process multiple texts;
        # I have not added multiple text processing here since it is covered by the translate_batch method
        response = None
        if is_input_valid(text):
            self._url_params["from"] = self._source
            self._url_params["to"] = self._target

            valid_microsoft_json = [{"text": text}]
            try:
                response = requests.post(
                    self._base_url,
                    params=self._url_params,
                    headers=self.headers,
                    json=valid_microsoft_json,
                    proxies=self.proxies,
                )
            except requests.exceptions.RequestException:
                exc_type, value, traceback = sys.exc_info()
                logging.warning(f"Returned error: {exc_type.__name__}")

            # Where Microsoft API responds with an api error, it returns a dict in response.json()
            if type(response.json()) is dict:
                error_message = response.json()["error"]
                raise MicrosoftAPIerror(error_message)
            # Where it responds with a translation, its response.json() is a list
            # e.g. [{'translations': [{'text':'Hello world!', 'to': 'en'}]}]
            elif type(response.json()) is list:
                all_translations = [
                    i["text"] for i in response.json()[0]["translations"]
                ]
                return "\n".join(all_translations)

    def translate_file(self, path: str, **kwargs) -> str:
        """
        translate from a file
        @param path: path to file
        @return: translated text
        """
        return self._translate_file(path, **kwargs)

    def translate_batch(self, batch: List[str], **kwargs) -> List[str]:
        """
        translate a batch of texts
        @param batch: list of texts to translate
        @return: list of translations
        """
        return self._translate_batch(batch, **kwargs)
