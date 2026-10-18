> [!IMPORTANT]
> **Using this fork as a Go library.** `src/pkg/engine` is the library API this
> fork exists to provide: open an encrypted Kopia repository with a
> caller-supplied key, back up Microsoft 365 data with caller-supplied tags and
> a progress callback, browse a backup point's folders and items, find items by
> name, and list, delete or run maintenance on backup points.
>
> - **Start here:** [`src/pkg/engine/README.md`](src/pkg/engine/README.md) — the call
>   shapes, the operational contract a caller has to know, the `go.work`
>   instructions for local development across repositories, and the `go get`
>   line that pins a release.
> - **Pin a release:** `go get github.com/twyouens/corso/src@v0.1.0` (the module
>   lives in `src/`, so its git tags carry that prefix: `src/v0.1.0`).
> - **Reference:** `cd src && GOWORK=off GO111MODULE=on go doc -all ./pkg/engine`.
> - **Examples:** `src/pkg/engine/example_test.go` (compiled) and
>   `src/cmd/backupexample` (runnable).

---

> [!NOTE]
> **This is an active fork maintained by [Daniel Puckett](https://github.com/DanielPuckett).**
>
> The original Corso project was archived by Alcionai. This fork exists because Corso remains the cornerstone of my Microsoft 365 backup strategy — backing up Exchange, OneDrive, and SharePoint to local disk. I am committed to the success and longevity of Corso as a reliable, self-hosted M365 backup solution.
>
> **Related projects:**
> - [corso-analysis.md](./corso-analysis.md) — Architecture review, API risk assessment, and Teams/Chats support status
> - [Corso-App](https://github.com/DanielPuckett/Corso-App) — Terminal UI for managing Corso snapshots (browse, export, restore, delete)
> - [M365Group-Backup](https://github.com/DanielPuckett/M365Group-Backup) — PowerShell scripts to backup/restore M365 group configuration (membership, owners, settings) that Corso does not cover

---

> [!NOTE] 
> **The Corso project is no longer actively maintained and has been archived**.

<p align="center">
    <img src="https://github.com/alcionai/corso/blob/main/website/static/img/corso_logo.svg?raw=true" alt="Corso Logo" width="100" />
</p>
<h1 align="center">Corso</h1>

[![Go Report Card](https://goreportcard.com/badge/github.com/alcionai/corso/src)](https://goreportcard.com/report/github.com/alcionai/corso/src)
[![Discord](https://img.shields.io/badge/discuss-discord-blue)](https://discord.gg/63DTTSnuhT)
[![License](https://img.shields.io/badge/License-Apache_2.0-green.svg)](https://opensource.org/licenses/Apache-2.0)
[![Contributor Covenant](https://img.shields.io/badge/Contributor%20Covenant-2.1-4baaaa.svg)](CODE_OF_CONDUCT.md)

Corso is the first open-source tool that aims to assist IT admins with the critical task of protecting their
Microsoft 365 data. It provides a reliable, secure, and efficient data protection engine. Admins decide where to store
the backup data and have the flexibility to perform backups of their desired service through an intuitive interface.
As Corso evolves, it can become a great building block for more complex data protection workflows.

**Corso is currently in Beta.**

Corso supports M365 Exchange and OneDrive with SharePoint and Teams support in active development. Coverage for more
services, possibly beyond M365, will expand based on the interest and needs of the community.

# Getting Started

See the [Corso Quickstart](https://corsobackup.io/docs/quickstart/) on our docs page.

# Building Corso

To learn more about working with the project source core and building Corso, see the
[Developer section](https://corsobackup.io/docs/developers/build) of the Corso Documentation.

# Roadmap

You can learn more about the Corso roadmap and how to interpret it [here](https://github.com/alcionai/corso-roadmap).

If you have feature requests, please file a [GitHub issue](https://github.com/alcionai/corso/issues/)
and attach the `enhancement` label to the issue.

# Contribution Guidelines

## Code of Conduct

It's important that our community is inclusive and respectful of everyone.
We ask that all Corso users and contributors take a few minutes to review our
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

Corso is licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full license text.
