__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"

from deep_translator.base import BaseTranslator

__engines__ = {
    translator.__name__.replace("Translator", "").lower(): translator
    for translator in BaseTranslator.__subclasses__()
}
