__copyright__ = "Copyright (C) 2020 Nidhal Baccouri"


class BaseError(Exception):
    """
    base error structure class
    """

    def __init__(self, val, message):
        """
        @param val: actual value
        @param message: message shown to the user
        """
        self.val = val
        self.message = message
        super().__init__()

    def __str__(self):
        return "{} --> {}".format(self.val, self.message)


class LanguageNotSupportedException(BaseError):
    """
    exception thrown if the user uses a language
    that is not supported by the deep_translator
    """

    def __init__(
        self, val, message="There is no support for the chosen language"
    ):
        super().__init__(val, message)


class NotValidPayload(BaseError):
    """
    exception thrown if the user enters an invalid payload
    """

    def __init__(
        self,
        val,
        message="text must be a valid text with maximum 5000 character,"
        "otherwise it cannot be translated",
    ):
        super(NotValidPayload, self).__init__(val, message)


class InvalidSourceOrTargetLanguage(BaseError):
    """
    exception thrown if the user enters an invalid payload
    """

    def __init__(self, val, message="Invalid source or target language!"):
        super(InvalidSourceOrTargetLanguage, self).__init__(val, message)


class TranslationNotFound(BaseError):
    """
    exception thrown if no translation was found for the text provided by the user
    """

    def __init__(
        self,
        val,
        message="No translation was found using the current translator. Try another translator?",
    ):
        super(TranslationNotFound, self).__init__(val, message)


class ElementNotFoundInGetRequest(BaseError):
    """
    exception thrown if the html element was not found in the body parsed by beautifulsoup
    """

    def __init__(
        self, val, message="Required element was not found in the API response"
    ):
        super(ElementNotFoundInGetRequest, self).__init__(val, message)


class NotValidLength(BaseError):
    """
    exception thrown if the provided text exceed the length limit of the translator
    """

    def __init__(self, val, min_chars, max_chars):
        message = f"Text length need to be between {min_chars} and {max_chars} characters"
        super(NotValidLength, self).__init__(val, message)


class RequestError(Exception):
    """
    exception thrown if an error occurred during the request call, e.g a connection problem.
    """

    def __init__(
        self,
        message="Request exception can happen due to an api connection error. "
        "Please check your connection and try again",
    ):
        self.message = message

    def __str__(self):
        return self.message


class MicrosoftAPIerror(Exception):
    """
    exception thrown if Microsoft API returns one of its errors
    """

    def __init__(self, api_message):
        self.api_message = str(api_message)
        self.message = "Microsoft API returned the following error"

    def __str__(self):
        return "{}: {}".format(self.message, self.api_message)


class TooManyRequests(Exception):
    """
    exception thrown if an error occurred during the request call, e.g a connection problem.
    """

    def __init__(
        self,
        message="Server Error: You made too many requests to the server."
        "According to google, you are allowed to make 5 requests per second"
        "and up to 200k requests per day. You can wait and try again later or"
        "you can try the translate_batch function",
    ):
        self.message = message

    def __str__(self):
        return self.message


class ServerException(Exception):
    """
    Default YandexTranslate exception from the official website
    """

    errors = {
        400: "ERR_BAD_REQUEST",
        401: "ERR_KEY_INVALID",
        402: "ERR_KEY_BLOCKED",
        403: "ERR_DAILY_REQ_LIMIT_EXCEEDED",
        404: "ERR_DAILY_CHAR_LIMIT_EXCEEDED",
        413: "ERR_TEXT_TOO_LONG",
        429: "ERR_TOO_MANY_REQUESTS",
        422: "ERR_UNPROCESSABLE_TEXT",
        500: "ERR_INTERNAL_SERVER_ERROR",
        501: "ERR_LANG_NOT_SUPPORTED",
        503: "ERR_SERVICE_NOT_AVAIBLE",
    }

    def __init__(self, status_code, *args):
        message = self.errors.get(status_code, "API server error")
        super(ServerException, self).__init__(message, *args)


class ApiKeyException(BaseError):
    """
    exception thrown if no ApiKey was provided
    """

    def __init__(self, env_var):
        msg = f"""
You have to pass your api_key!
You can do this by passing the key as a parameter/argument to the translator class
or by setting the environment variable {env_var}

Example: export {env_var}="your_api_key"
"""

        super().__init__(None, msg)


class AuthorizationException(Exception):
    def __init__(self, api_key, *args):
        msg = "Unauthorized access with the api key " + api_key
        super().__init__(msg, *args)


class BaiduAPIerror(Exception):
    """
    exception thrown if Baidu API returns one of its errors
    """

    def __init__(self, api_message):
        self.api_message = str(api_message)
        self.message = "Baidu API returned the following error"

    def __str__(self):
        return "{}: {}".format(self.message, self.api_message)
