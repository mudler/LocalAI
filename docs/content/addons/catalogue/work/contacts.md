+++
disableToc = false
title = "Contacts"
weight = 50
aliases = ["/features/contacts/"]
+++

The Contacts add-on gives you an address book inside LocalAI. Ask a chat or a [saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}}) "what is Marco's number?" or "add Anna, anna@example.org" and it reads or changes the book. There is no account to connect, no sync and no setup: install the add-on and use it.

## What it keeps

There is **one address book for each user**, shared by all of that user's chats and agents. It is one SQLite file in the data directory of the add-on. A contact has:

- a name, and an organisation;
- phone numbers and email addresses, each with a free label such as `mobile`, `home` or `work` (up to 10 of each);
- a birthday, as `YYYY-MM-DD`, or `--MM-DD` when the year is not known;
- an address and notes, each as free text.

There is no sync with another service, no CardDAV, no groups, no photos and no merging of duplicates. Import of vCard files is not part of this version. You can export the whole book as one vCard file from the page.

## The page

Open **Contacts** in the add-ons. It has a search box, the list of contacts, the detail of the one you pick, a form to add or change a contact, and the export button. The search matches every word you type against the name, organisation, phone numbers, email addresses, address and notes. It ignores case and accents, and a phone number matches however it is spaced. The page speaks the language of the web UI.

## What the agents can do

| Tool | Does |
|---|---|
| `contacts-search` | Finds contacts by words, and gives a short card for each match with the phones and emails. |
| `contact-get` | Reads one contact in full. |
| `contact-create`, `contact-update`, `contact-delete` | Add, change and delete a contact. A `phones` or `emails` list in an update replaces the whole list. |

The reads run at once. The writes are action tools: they run at once, as the user, with no confirmation card, and an unattended run (a trigger or a job) needs the `act.addons` grant for them. The add-on ships a skill that tells the model when to use them.

In a messaging chat, the owner and the senders paired with the bot can read and change the book. Every other sender the bot lets in can read it but not change it (the `tools.write` rule, see [Add-ons that keep the owner's own data]({{% relref "/addons/contract-reference" %}}#add-ons-that-keep-the-owners-own-data)). The book is private to its user: a tool call can never name another user's book.

## Limits

A name has at most 200 characters, an organisation 200, a label 40, a phone number 40, an email address 254, an address 500 and notes 4000. One user keeps at most 2000 contacts.

A vCard export is a vCard 3.0 file. A birthday without a year is written as `--MM-DD`, which some programs do not read.
