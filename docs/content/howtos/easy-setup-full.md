+++
disableToc = false
title = "Easy Demo - Full Chat Python AI"
weight = 2
+++

{{% notice Note %}}
- You will need about 10gb of RAM Free
- You will need about 15gb of space free on C drive for ``Docker-compose``
{{% /notice %}}

This is for `Linux`, `Mac OS`, or `Windows` Hosts. - [Docker Desktop](https://docs.docker.com/engine/install/), [Python 3.11](https://www.python.org/downloads/release/python-3110/), [Git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)

Linux Hosts:

There is a Full_Auto installer compatible with some types of Linux distributions, feel free to use them, but note that they may not fully work. If you need to install something, please use the links at the top.

```bash
git clone https://github.com/lunamidori5/localai-lunademo.git

cd localai-lunademo

#Pick your type of linux for the Full Autos, if you already have python, docker, and docker-compose installed skip this chmod. But make sure you chmod the setup_linux file.

chmod +x Full_Auto_setup_Debian.sh or chmod +x Full_Auto_setup_Ubutnu.sh

chmod +x Setup_Linux.sh

#Make sure to install cuda to your host OS and to Docker if you plan on using GPU

./(the setupfile you wish to run)
```

Windows Hosts:

```batch
REM Make sure you have git, docker-desktop, and python 3.11 installed

git clone https://github.com/lunamidori5/localai-lunademo.git

cd localai-lunademo

call Setup.bat
```

MacOS Hosts: 
- I need some help working on a MacOS Setup file, if you are willing to help out, please contact Luna Midori on [discord](https://discord.com/channels/1096914990004457512/1099364883755171890/1147591145057157200) or put in a PR on [Luna Midori's github](https://github.com/lunamidori5/localai-lunademo).

Video How Tos 

- Ubuntu - ``COMING SOON``
- Debian - ``COMING SOON``
- Windows - ``COMING SOON``
- MacOS - ``PLANED - NEED HELP``

Enjoy localai! (If you need help contact Luna Midori on [Discord](https://discord.com/channels/1096914990004457512/1099364883755171890/1147591145057157200))

{{% notice Issues %}}
- Trying to run ``Setup.bat`` or ``Setup_Linux.sh`` from `Git Bash` on Windows is not working. (Somewhat fixed)
- Running over `SSH` or other remote command line based apps may bug out, load slowly, or crash.
{{% /notice %}}
