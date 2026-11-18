+++
disableToc = false
title = "Contacts"
weight = 50
aliases = ["/features/contacts/"]
+++

## At a glance

| Topic | Detail |
|---|---|
| What it adds | One address book for each user, inside LocalAI. A chat or a saved agent looks up and keeps contacts. There is no account to connect. |
| Needs | Nothing. There's no account to connect. |

Ask a chat or a [saved agent]({{% relref "/features/chats-and-agents/chats-agents" %}}) "what is Marco's number?" or "add Anna, anna@example.org" and it reads or changes your address book. Install it and ask. There's nothing to connect. Writes run at once, with no confirmation card.

## What it keeps

There is **one address book for each user**, shared by all of that user's chats and agents. It is one SQLite file in the data directory of the add-on. A contact has:

- a name, and an organisation;
- phone numbers and email addresses, each with a free label such as `mobile`, `home` or `work` (up to 10 of each);
- a birthday, as `YYYY-MM-DD`, or `--MM-DD` when the year is not known;
- an address and notes, each as free text.

It doesn't sync with another service, support CardDAV, groups or photos, or merge duplicates. It can't import vCard files yet. You can export the whole book as one vCard file from the page.

## The page

Open **Contacts** in the add-ons. It has a search box, the list of contacts, the detail of the one you pick, a form to add or change a contact, and the export button. The search matches every word you type against the name, organisation, phone numbers, email addresses, address and notes. It ignores case and accents, and a phone number matches however it is spaced. The page speaks the language of the web UI.

## What the agents can do

| Tool | Does |
|---|---|
| `contacts-search` | Finds contacts by words, and gives a short card for each match with the phones and emails. |
| `contact-get` | Reads one contact in full. |
| `contact-create`, `contact-update`, `contact-delete` | Add, change and delete a contact. A `phones` or `emails` list in an update replaces the whole list. |

Reads and writes both run immediately, as the signed-in user, with no confirmation card. A trigger or a job can write only with the `act.addons` grant. The add-on ships a skill that tells the model when to use the tools.

In a messaging chat, the owner and the senders paired with the bot can read and change the book. Every other sender the bot lets in can read it but not change it (the `tools.write` rule, see [Add-ons that keep the owner's own data]({{% relref "/addons/contract-reference/owner-forwarding#add-ons-that-keep-the-owners-own-data" %}})). The book is private to its user: a tool call can never name another user's book.

## Limits

A name has at most 200 characters, an organisation 200, a label 40, a phone number 40, an email address 254, an address 500 and notes 4000. One user keeps at most 2000 contacts.

A vCard export is a vCard 3.0 file. A birthday without a year is written as `--MM-DD`, which some programs do not read.
