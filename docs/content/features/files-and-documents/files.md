+++
url = "/features/files/"
disableToc = false
title = "Files and shared folders"
description = "Keep your files in LocalAI, share folders with other people, and use the files in a chat."
weight = 10
+++

LocalAI keeps a workspace for each user. It holds your files and folders, the files that a chat reads or writes, and a Trash with a history. The **Files** page in the sidebar is the browser for it.

{{% callout style="note" %}}
Files need the chats of the server. The chats and their files must be on. See [Chats]({{% relref "/features/chats-and-agents/chats" %}}). On a server with accounts, an administrator can turn the `chats` feature off for a user. That user then has no Files page.
{{% /callout %}}

## Upload and organise

1. Open **Files** in the sidebar.
2. Choose **Upload**, or drop files or folders on the page, or paste with Ctrl+V.
3. Choose **New folder** to make a folder. Drag rows onto a folder to move them.

You can upload files of any type. A file can be 256 MiB at most. An administrator can set a smaller limit with `--upload-limit`. If a file has the name of a file that is in the folder already, the page asks you to replace it, keep both or skip it.

Each row has a menu. It offers preview, download, rename, move and **Move to Trash**. Nothing is erased until you empty the Trash. The **Trash** tab restores a file, and the **History** tab undoes a change.

LocalAI keeps two folders for itself. `LocalAI/` holds your settings. `Chats/` holds one folder for each chat. They show at the end of the list under **Managed by LocalAI**.

## Find a file

Type in the box to filter the open folder by name. Press Enter to search the names and the text of all your files. The search reads the text of PDF, Word, Excel and PowerPoint files too. A scanned PDF has no text layer, so it is found by name only. Details are in [Search inside documents]({{% relref "/features/chats-and-agents/chats#search-inside-documents" %}}).

## Unpack an archive

Choose **Extract here** in the menu of an archive. The server unpacks it into a new folder next to the archive. One **Undo** removes the whole result. ZIP and TAR archives work. An administrator can allow 7z and RAR, but the readers for them have a known risk. Read [Extract an archive]({{% relref "/features/chats-and-agents/chats#extract-an-archive" %}}) before you allow them.

## Use a file in a chat

A chat can use your files in two ways.

- Attach a file to a message. The file is saved in the folder of the chat, and the model gets its path.
- Ask the model to work with your files. A chat has file tools to list, read, search, write and edit files. They change your files like you do, so every change is in the history and you can undo it.

The model can read the text of PDF, Word, Excel and PowerPoint files. It can also look at images and transcribe audio when a model for that is installed. To run commands or code on the files, install the [Sandbox]({{% relref "/addons/catalogue/work/sandbox" %}}) add-on. To make Word, Excel, PowerPoint and PDF files, see [Office documents]({{% relref "/features/files-and-documents/office" %}}).

A saved agent can work in chosen folders only, each as read or write. Without folders, an agent works on all your files. See [Folders of an agent]({{% relref "/features/chats-and-agents/chats-agents#folders-of-an-agent" %}}).

## Share folders

Your files are private to your account until you share something. There are two kinds of sharing.

**A shared folder** is made by an administrator. It is a folder that several people use. The administrator chooses the members: a person, or **Everyone**, each with **Can read** or **Can edit**. Members see the folder in the **Shared** tab of Files. Their chats see it at `/workspace/shared-<name>`, and it is read only for a reader. A person who is not a member sees nothing.

**A folder of your home** can be shared by you. Choose **Share...** in the menu of the folder, then add a person or **Everyone**, with **Can read** or **Can edit**. You can share a folder inside your home. You cannot share the home itself, or the folders that LocalAI keeps. Sharing needs a server with accounts.

Sharing is a change that only a person makes in their own browser session. A model, an agent or an add-on cannot share a folder. For the rules, see [Users and sharing]({{% relref "/concepts/users-and-sharing" %}}) and [Shared folders]({{% relref "/features/chats-and-agents/chats#shared-folders" %}}).

## Limits

| Limit | Value |
|---|---|
| One file | 256 MiB, or the `--upload-limit` value when it is smaller |
| Files in one message | 1 GiB in all |
| Shared folders on a server | 100, with 200 members each |
| Archive extraction | 1 GiB, 20,000 entries and 10 minutes by default |

## Next

- [Chats]({{% relref "/features/chats-and-agents/chats" %}}) has the full reference of the files API.
- [Make LocalAI yours]({{% relref "/features/chats-and-agents/make-localai-yours" %}}) explains the `LocalAI/` folder.
