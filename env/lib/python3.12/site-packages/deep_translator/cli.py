"""Console script for deep_translator."""

__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

import argparse
from typing import Optional

from deep_translator.engines import __engines__


class CLI(object):
    translators_dict = __engines__
    translator = None

    def __init__(self, custom_args: Optional[list] = None):
        self.custom_args = custom_args
        self.args = self.parse_args()
        translator_class = self.translators_dict.get(
            self.args.translator, None
        )
        if not translator_class:
            raise Exception(
                f"Translator {self.args.translator} is not supported."
                f"Supported translators: {list(self.translators_dict.keys())}"
            )
        self.translator = translator_class(
            source=self.args.source, target=self.args.target
        )

    def translate(self) -> None:
        """
        function used to provide translations from the parsed terminal arguments
        @return: None
        """
        res = self.translator.translate(self.args.text)
        print(f"Translation from {self.args.source} to {self.args.target}")
        print("-" * 50)
        print(f"Translation result: {res}")

    def get_supported_languages(self) -> None:
        """
        function used to return the languages supported by the translator service
        from the parsed terminal arguments
        @return: None
        """

        translator_supported_languages = (
            self.translator.get_supported_languages(as_dict=True)
        )
        print(f"Languages supported by '{self.args.translator}' are :\n")
        print(translator_supported_languages)

    def parse_args(self) -> argparse.Namespace:
        """
        function responsible for parsing terminal arguments and provide
        them for further use in the translation process
        """
        parser = argparse.ArgumentParser(
            add_help=True,
            description="Official CLI for deep-translator",
            usage="dt --help",
        )

        parser.add_argument(
            "--translator",
            "-trans",
            default="google",
            type=str,
            help="name of the translator you want to use",
        )
        parser.add_argument(
            "--source",
            "-src",
            default="auto",
            type=str,
            help="source language to translate from",
        )
        parser.add_argument(
            "--target", "-tg", type=str, help="target language to translate to"
        )
        parser.add_argument(
            "--text", "-txt", type=str, help="text you want to translate"
        )
        parser.add_argument(
            "--languages",
            "-lang",
            action="store_true",
            help="all the languages available with the translator"
            "Run the command deep_translator -trans <translator service> -lang",
        )
        parsed_args = (
            parser.parse_args(self.custom_args)
            if self.custom_args
            else parser.parse_args()
        )
        return parsed_args

    def run(self) -> None:
        if self.args.languages:
            self.get_supported_languages()
        else:
            self.translate()
