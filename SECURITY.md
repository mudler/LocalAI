# Security Policy

## Introduction

At LocalAI, we take the security of our software seriously. We understand the importance of protecting our community from vulnerabilities and are committed to ensuring the safety and security of our users.

## Supported Versions

We provide support and updates for certain versions of our software. The following table outlines which versions are currently supported with security updates:

| Version Series | Support Level | Details |
| -------------- | ------------- | ------- |
| 3.x | :white_check_mark: Actively supported | Full security updates and bug fixes for the latest minor versions. |
| 2.x | :warning: Security fixes only | Critical security patches only, until **December 31, 2026**. |
| 1.x | :x: End-of-life (EOL) | No longer supported as of **January 1, 2024**. No security fixes will be provided. |

### What each support level means

- **Actively supported (3.x):** Receives all security updates, bug fixes, and new features. Users should stay on the latest 3.x minor release for the best protection.
- **Security fixes only (2.x):** Receives only critical security patches (e.g., remote code execution, authentication bypass, data exposure). No bug fixes or new features. Support ends December 31, 2026.
- **End-of-life (1.x):** No updates of any kind. Users on 1.x are strongly encouraged to upgrade immediately, as known vulnerabilities will not be patched.

### Migrating from older versions

If you are running an unsupported or soon-to-be-unsupported version, we recommend upgrading as soon as possible:

- **From 1.x to 3.x:** Version 1.x reached end-of-life on January 1, 2024. Review the [release notes](https://github.com/mudler/LocalAI/releases) for breaking changes across major versions, and upgrade directly to the latest 3.x release.
- **From 2.x to 3.x:** While 2.x still receives critical security patches until December 31, 2026, we recommend planning your migration to 3.x to benefit from ongoing improvements and full support.

Please ensure that you are using a supported version to receive the latest security updates.

## Reporting a Vulnerability

We encourage the responsible disclosure of any security vulnerabilities. If you believe you've found a security issue in our software, we kindly ask you to follow the steps below to report it to us:

1. **Email Us:** Send an email to [security@localai.io](mailto:security@localai.io) with a detailed report. Please do not disclose the vulnerability publicly or to any third parties before it has been addressed by us.

2. **Expect a Response:** We aim to acknowledge receipt of vulnerability reports within 48 hours. Our security team will review your report and work closely with you to understand the impact and ensure a thorough investigation.

3. **Collaboration:** If the vulnerability is accepted, we will work with you and our community to address the issue promptly. We'll keep you informed throughout the resolution process and may request additional information or collaboration.

4. **Disclosure:** Once the vulnerability has been resolved, we encourage a coordinated disclosure. We believe in transparency and will work with you to ensure that our community is informed in a responsible manner.

## Use of Third-Party Platforms

As a Free and Open Source Software (FOSS) organization, we do not offer monetary bounties. However, researchers who wish to report vulnerabilities can also do so via [Huntr](https://huntr.dev/bounties), a platform that recognizes contributions to open source security.

## Contact

For any security-related inquiries beyond vulnerability reporting, please contact us at [security@localai.io](mailto:security@localai.io).

## Acknowledgments

We appreciate the efforts of those who contribute to the security of our project. Your responsible disclosure is invaluable to the safety and integrity of LocalAI.

Thank you for helping us keep LocalAI secure.
