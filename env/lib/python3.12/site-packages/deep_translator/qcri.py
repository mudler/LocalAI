__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

import os
from typing import List, Optional

import requests

from deep_translator.base import BaseTranslator
from deep_translator.constants import (
    BASE_URLS,
    QCRI_ENV_VAR,
    QCRI_LANGUAGE_TO_CODE,
)
from deep_translator.exceptions import (
    ApiKeyException,
    ServerException,
    TranslationNotFound,
)
from deep_translator.validate import request_failed


class QcriTranslator(BaseTranslator):
    """
    class that wraps functions, which use the QRCI translator under the hood to translate word(s)
    """

    def __init__(
        self,
        source: str = "en",
        target: str = "en",
        api_key: Optional[str] = os.getenv(QCRI_ENV_VAR, None),
        **kwargs,
    ):
        """
        @param api_key: your qrci api key.
        Get one for free here https://mt.qcri.org/api/v1/ref
        """

        if not api_key:
            raise ApiKeyException(QCRI_ENV_VAR)

        self.api_key = api_key
        self.api_endpoints = {
            "get_languages": "getLanguagePairs",
            "get_domains": "getDomains",
            "translate": "translate",
        }

        self.params = {"key": self.api_key}
        super().__init__(
            base_url=BASE_URLS.get("QCRI"),
            source=source,
            target=target,
            languages=QCRI_LANGUAGE_TO_CODE,
            **kwargs,
        )

    def _get(
        self,
        endpoint: str,
        params: Optional[dict] = None,
        return_text: bool = True,
    ):
        if not params:
            params = self.params
        try:
            res = requests.get(
                self._base_url.format(endpoint=self.api_endpoints[endpoint]),
                params=params,
            )
            return res.text if return_text else res
        except Exception as e:
            raise e

    @property
    def languages(self):
        return self.get_supported_languages()

    def get_domains(self):
        domains = self._get("get_domains")
        return domains

    @property
    def domains(self):
        return self.get_domains()

    def translate(self, text: str, **kwargs) -> str:
        params = {
            "key": self.api_key,
            "langpair": f"{self._source}-{self._target}",
            "domain": kwargs["domain"],
            "text": text,
        }
        try:
            response = self._get("translate", params=params, return_text=False)
        except ConnectionError:
            raise ServerException(503)

        else:
            if request_failed(status_code=response.status_code):
                ServerException(response.status_code)
            else:
                res = response.json()
                translation = res.get("translatedText")
                if not translation:
                    raise TranslationNotFound(text)
                return translation

    def translate_file(self, path: str, **kwargs) -> str:
        return self._translate_file(path, **kwargs)

    def translate_batch(self, batch: List[str], **kwargs) -> List[str]:
        """
        translate a batch of texts
        @domain: domain
        @param batch: list of texts to translate
        @return: list of translations
        """
        return self._translate_batch(batch, **kwargs)
