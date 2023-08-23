"""
This is the Continue configuration file.

See https://continue.dev/docs/customization to learn more.
"""

import subprocess

from continuedev.src.continuedev.core.main import Step
from continuedev.src.continuedev.core.sdk import ContinueSDK
from continuedev.src.continuedev.core.models import Models
from continuedev.src.continuedev.core.config import CustomCommand, SlashCommand, ContinueConfig
from continuedev.src.continuedev.plugins.context_providers.github import GitHubIssuesContextProvider
from continuedev.src.continuedev.plugins.context_providers.google import GoogleContextProvider
from continuedev.src.continuedev.plugins.policies.default import DefaultPolicy
from continuedev.src.continuedev.libs.llm.openai import OpenAI, OpenAIServerInfo
from continuedev.src.continuedev.libs.llm.ggml import GGML

from continuedev.src.continuedev.plugins.steps.open_config import OpenConfigStep
from continuedev.src.continuedev.plugins.steps.clear_history import ClearHistoryStep
from continuedev.src.continuedev.plugins.steps.feedback import FeedbackStep
from continuedev.src.continuedev.plugins.steps.comment_code import CommentCodeStep
from continuedev.src.continuedev.plugins.steps.share_session import ShareSessionStep
from continuedev.src.continuedev.plugins.steps.main import EditHighlightedCodeStep
from continuedev.src.continuedev.plugins.context_providers.search import SearchContextProvider
from continuedev.src.continuedev.plugins.context_providers.diff import DiffContextProvider
from continuedev.src.continuedev.plugins.context_providers.url import URLContextProvider

class CommitMessageStep(Step):
    """
    This is a Step, the building block of Continue.
    It can be used below as a slash command, so that
    run will be called when you type '/commit'.
    """
    async def run(self, sdk: ContinueSDK):

        # Get the root directory of the workspace
        dir = sdk.ide.workspace_directory

        # Run git diff in that directory
        diff = subprocess.check_output(
            ["git", "diff"], cwd=dir).decode("utf-8")

        # Ask the LLM to write a commit message,
        # and set it as the description of this step
        self.description = await sdk.models.default.complete(
            f"{diff}\n\nWrite a short, specific (less than 50 chars) commit message about the above changes:")


config = ContinueConfig(

    # If set to False, we will not collect any usage data
    # See here to learn what anonymous data we collect: https://continue.dev/docs/telemetry
    allow_anonymous_telemetry=True,

    models = Models(
        default = OpenAI(
            api_key = "my-api-key",
            model = "gpt-3.5-turbo",
            openai_server_info = OpenAIServerInfo(
                api_base = "http://localhost:8080",
                model = "gpt-3.5-turbo"
            )
        )
    ),
    # Set a system message with information that the LLM should always keep in mind
    # E.g. "Please give concise answers. Always respond in Spanish."
    system_message=None,

    # Set temperature to any value between 0 and 1. Higher values will make the LLM
    # more creative, while lower values will make it more predictable.
    temperature=0.5,

    # Custom commands let you map a prompt to a shortened slash command
    # They are like slash commands, but more easily defined - write just a prompt instead of a Step class
    # Their output will always be in chat form
    custom_commands=[
        # CustomCommand(
        #     name="test",
        #     description="Write unit tests for the higlighted code",
        #     prompt="Write a comprehensive set of unit tests for the selected code. It should setup, run tests that check for correctness including important edge cases, and teardown. Ensure that the tests are complete and sophisticated. Give the tests just as chat output, don't edit any file.",
        # )
    ],

    # Slash commands let you run a Step from a slash command
    slash_commands=[
        # SlashCommand(
        #     name="commit",
        #     description="This is an example slash command. Use /config to edit it and create more",
        #     step=CommitMessageStep,
        # )
        SlashCommand(
            name="edit",
            description="Edit code in the current file or the highlighted code",
            step=EditHighlightedCodeStep,
        ),
        SlashCommand(
            name="config",
            description="Customize Continue - slash commands, LLMs, system message, etc.",
            step=OpenConfigStep,
        ),
        SlashCommand(
            name="comment",
            description="Write comments for the current file or highlighted code",
            step=CommentCodeStep,
        ),
        SlashCommand(
            name="feedback",
            description="Send feedback to improve Continue",
            step=FeedbackStep,
        ),
        SlashCommand(
            name="clear",
            description="Clear step history",
            step=ClearHistoryStep,
        ),
        SlashCommand(
            name="share",
            description="Download and share the session transcript",
            step=ShareSessionStep,
        )
    ],

    # Context providers let you quickly select context by typing '@'
    # Uncomment the following to
    # - quickly reference GitHub issues
    # - show Google search results to the LLM
    context_providers=[
        # GitHubIssuesContextProvider(
        #     repo_name="<your github username or organization>/<your repo name>",
        #     auth_token="<your github auth token>"
        # ),
        # GoogleContextProvider(
        #     serper_api_key="<your serper.dev api key>"
        # )
        SearchContextProvider(),
        DiffContextProvider(),
        URLContextProvider(
            preset_urls = [
                # Add any common urls you reference here so they appear in autocomplete
            ]
        )
    ],

    # Policies hold the main logic that decides which Step to take next
    # You can use them to design agents, or deeply customize Continue
    policy=DefaultPolicy()
)
