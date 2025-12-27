# Licensing and Legal Considerations

## TL;DR

**You are safe to open-source and sell SesameFS commercially.** This project implements a Seafile-*compatible* API but contains no Seafile code. API compatibility alone does not create licensing obligations.

## SesameFS License

SesameFS is an independent project. You can choose any license you want, such as:
- **MIT** or **Apache 2.0** - Permissive, allows commercial use
- **AGPLv3** - Copyleft, requires source disclosure for network services
- **Proprietary** - Closed source, commercial only

## Seafile's License

Seafile server core is licensed under [AGPLv3](https://github.com/haiwen/seafile-server). However, this is **irrelevant** to SesameFS because:

1. **We don't use any Seafile code** - SesameFS is written from scratch in Go
2. **We don't copy Seafile's implementation** - We only implement compatible API endpoints
3. **API compatibility is not copyright infringement** - See legal precedents below

## Legal Precedents for API Compatibility

### Clean-Room Implementation

SesameFS uses a "clean-room" approach:
- API behavior was documented from public Seafile documentation
- Implementation was written independently without reference to Seafile source code
- No Seafile code was copied or derived

This approach has been legally validated in numerous cases:

### Key Legal Cases

1. **[Google v. Oracle (2021)](https://en.wikipedia.org/wiki/Google_LLC_v._Oracle_America,_Inc.)** - US Supreme Court ruled that Google's use of Java APIs was fair use, even for commercial purposes. The court emphasized that APIs serve a functional purpose and copying them for interoperability is permissible.

2. **[Sony v. Connectix (1999)](https://en.wikipedia.org/wiki/Sony_Computer_Entertainment,_Inc._v._Connectix_Corp.)** - Established that reverse engineering for compatibility is legal. Connectix's PlayStation emulator was ruled legal despite being commercially sold.

3. **[Sega v. Accolade (1992)](https://en.wikipedia.org/wiki/Sega_Enterprises,_Ltd._v._Accolade,_Inc.)** - Ruled that reverse engineering for interoperability is fair use, even for commercial competitors.

4. **[Phoenix Technologies / AMI BIOS](https://en.wikipedia.org/wiki/Clean-room_design)** - Clean-room implementations of IBM PC BIOS were sold commercially to PC clone manufacturers, establishing the validity of API-compatible implementations.

### Why API Compatibility is Legal

From [legal analysis](https://www.scoredetect.com/blog/posts/ensuring-copyright-protection-for-software-apis-legal-insights):

> "Leveraging APIs for interoperability is typically permissible. APIs serve functional purposes, and courts have recognized that copying APIs for compatibility does not constitute copyright infringement when done properly."

Key principles:
- **APIs are functional** - They describe *what* something does, not *how*
- **Interoperability is valuable** - Allows software ecosystems to work together
- **Clean-room implementations are protected** - No actual code is copied

## What You CAN Do

✅ **Open-source SesameFS** under any license (MIT, Apache 2.0, GPL, etc.)
✅ **Sell SesameFS commercially** as a product or service
✅ **Advertise Seafile compatibility** - You're providing interoperability
✅ **Modify and extend** the Seafile-compatible API
✅ **Use any backend** (S3, Cassandra, etc.) - implementation is yours

## What You Should NOT Do

❌ **Copy Seafile source code** - This would create licensing obligations
❌ **Claim to be Seafile** - Trademark issues (use "Seafile-compatible" instead)
❌ **Distribute Seafile binaries** - Their license applies to their code

## Recommended Practices

### 1. Clear Attribution
In your documentation, be clear that SesameFS is:
- An independent implementation
- Seafile-*compatible*, not Seafile itself
- Not affiliated with Seafile Ltd.

Example disclaimer:
> "SesameFS implements a Seafile-compatible API for interoperability purposes. SesameFS is not affiliated with, endorsed by, or connected to Seafile Ltd. or the Seafile project. Seafile is a trademark of Seafile Ltd."

### 2. License Choice Recommendations

| Goal | Recommended License |
|------|---------------------|
| Maximum adoption | MIT or Apache 2.0 |
| Prevent proprietary forks | AGPLv3 |
| Commercial-only product | Proprietary |
| Dual licensing (open + commercial) | AGPLv3 + Commercial |

### 3. Trademark Considerations

- Don't use "Seafile" in your product name
- Use phrases like "Seafile-compatible" or "works with Seafile clients"
- Don't use Seafile's logo or branding

## Similar Precedents in the Industry

Many successful projects implement compatible APIs:

| Project | Compatible With | License |
|---------|----------------|---------|
| MinIO | Amazon S3 | AGPLv3 |
| MariaDB | MySQL | GPLv2 |
| CockroachDB | PostgreSQL | BSL/Apache |
| Gitea | GitHub API (partial) | MIT |
| Nextcloud | ownCloud | AGPLv3 |

These projects prove that API compatibility is a valid and legal approach.

## Conclusion

SesameFS is a clean-room implementation of a Seafile-compatible API. You have full rights to:
- License it however you choose
- Use it commercially
- Sell it as a product or service
- Open-source it

The key is that you're implementing *compatible behavior*, not copying *code*. This is a well-established legal practice in the software industry.

## References

- [Clean-room design - Wikipedia](https://en.wikipedia.org/wiki/Clean-room_design)
- [Seafile Server License (AGPLv3)](https://github.com/haiwen/seafile-server)
- [API Copyright - Legal Insights](https://www.scoredetect.com/blog/posts/ensuring-copyright-protection-for-software-apis-legal-insights)
- [Seafile License Change Announcement](https://forum.seafile.com/t/seafile-server-core-changes-license-to-agplv3-and-will-be-separated-from-client-source-code/321)
