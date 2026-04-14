# Encryption Flow Diagrams

> How to read: Green nodes are security mechanisms. Blue nodes are crypto operations.
> Yellow highlights the weak PBKDF2 path. Red indicates data exposure risk.

### How to read colors

| Color | Meaning |
|-------|---------|
| **Green** | Security check or safety mechanism |
| **Blue** | Cryptographic operation |
| **Yellow** | Weak path (PBKDF2 compat) |
| **Red** | Exposed or at-risk data |

---

## 1. Library Unlock (Decrypt Session)

```mermaid
flowchart TD
    User["User enters password"]
    User --> API["POST /set-password"]
    API --> ReadDB["Read from Cassandra:<br/>magic, magic_strong, salt,<br/>enc_version, random_key"]

    ReadDB --> TryStrong["Argon2id path<br/>3 iter, 64MB memory"]
    TryStrong --> MagicStrong["Compute magic<br/>HMAC-SHA256(key, repo_id)"]
    MagicStrong --> CompareStrong["subtle.ConstantTimeCompare<br/>vs stored magic_strong"]
    CompareStrong -->|Match| DecryptStrong["AES-256-CBC decrypt<br/>random_key_strong"]

    CompareStrong -->|No match| TryWeak["PBKDF2 fallback<br/>1000 iterations"]
    TryWeak --> MagicWeak["Compute magic<br/>hex(derived_key)"]
    MagicWeak --> CompareWeak["subtle.ConstantTimeCompare<br/>vs stored magic"]
    CompareWeak -->|Match| DecryptWeak["AES-256-CBC decrypt<br/>random_key"]
    CompareWeak -->|No match| Wrong["400: Wrong password"]

    DecryptStrong --> SecondKDF["2nd PBKDF2 derivation<br/>secret_key → file_key + IV"]
    DecryptWeak --> SecondKDF

    SecondKDF --> Store["Store in memory<br/>DecryptSessions[user:repo]<br/>TTL = 1 hour"]
    Store --> OK["200: Library unlocked"]

    style TryStrong fill:#28a745,color:#fff
    style CompareStrong fill:#28a745,color:#fff
    style CompareWeak fill:#28a745,color:#fff
    style TryWeak fill:#ffc107,color:#000
    style DecryptStrong fill:#17a2b8,color:#fff
    style DecryptWeak fill:#17a2b8,color:#fff
    style SecondKDF fill:#17a2b8,color:#fff
    style Wrong fill:#dc3545,color:#fff
```

---

## 2. Block Encryption on Upload

```mermaid
flowchart TD
    Plain["Plaintext block"]

    Plain --> Which{"Client type?"}
    Which -->|"Seafile desktop"| SeaEnc["EncryptBlockSeafile<br/>AES-256-CBC<br/>Derived IV (static per lib)"]
    Which -->|"Web / OnlyOffice"| ModEnc["EncryptBlock<br/>AES-256-CBC<br/>Random IV per block"]

    SeaEnc --> SeaOut["Output: [ciphertext]<br/>No IV prepended"]
    ModEnc --> ModOut["Output: [16B IV | ciphertext]<br/>IV prepended"]

    SeaOut --> SHA["SHA-256(ciphertext)<br/>= S3 storage key"]
    ModOut --> SHA
    SHA --> S3["Store in S3"]

    style SeaEnc fill:#ffc107,color:#000
    style ModEnc fill:#28a745,color:#fff
    style SHA fill:#17a2b8,color:#fff
```

---

## 3. Block Decryption on Download

```mermaid
flowchart TD
    S3["Fetch from S3<br/>(encrypted bytes)"]
    S3 --> Check{"Looks encrypted?<br/>len >= 32, aligned"}
    Check -->|No| Legacy["Return as-is<br/>(legacy unencrypted)"]

    Check -->|Yes| Extract["Extract IV from<br/>first 16 bytes"]
    Extract --> AES["AES-256-CBC decrypt<br/>with file_key"]
    AES --> Pad{"Valid PKCS7<br/>padding?"}
    Pad -->|Yes| Plain["Return plaintext"]
    Pad -->|No| Fallback["Return original<br/>(wasn't encrypted)"]

    style Check fill:#28a745,color:#fff
    style AES fill:#17a2b8,color:#fff
    style Legacy fill:#ffc107,color:#000
    style Fallback fill:#ffc107,color:#000
```

---

## 4. Password Change (No Re-encryption)

```mermaid
flowchart TD
    Old["Verify old password<br/>(same as unlock flow)"]
    Old --> Decrypt["Decrypt file_key<br/>using old password"]
    Decrypt --> NewSalt["Generate new<br/>32-byte random salt"]
    NewSalt --> NewKeys["Derive new keys<br/>PBKDF2 + Argon2id<br/>from new password"]
    NewKeys --> ReWrap["Re-encrypt SAME file_key<br/>with new keys"]
    ReWrap --> NewMagic["Compute new magic tokens<br/>(both weak + strong)"]
    NewMagic --> UpdateDB["Update Cassandra:<br/>salt, magic, magic_strong,<br/>random_key, random_key_strong"]
    UpdateDB --> Done["Done<br/>All blocks remain valid<br/>No re-encryption needed"]

    style Old fill:#28a745,color:#fff
    style Decrypt fill:#17a2b8,color:#fff
    style ReWrap fill:#17a2b8,color:#fff
    style Done fill:#28a745,color:#fff
```

---

## 5. Decrypt Session Expiry During Download

```mermaid
flowchart TD
    Start["Download starts<br/>fileKey captured by goroutine"]
    Start --> B1["Prefetch block 1<br/>uses captured fileKey"]
    B1 --> Write1["Write block 1 to client"]
    Write1 --> B2["Prefetch block 2"]

    B2 --> Expiry["⏰ 1 hour passes<br/>Session expires in manager"]

    Expiry --> B3["Prefetch block 3<br/>Still uses captured fileKey<br/>(Go closure holds reference)"]
    B3 --> Write3["Write block 3"]
    Write3 --> Done["Download completes<br/>successfully"]

    Expiry --> NewReq["New download request<br/>calls GetFileKey()"]
    NewReq --> Nil["Returns nil<br/>(session expired)"]
    Nil --> R403["403: not unlocked"]

    style Expiry fill:#ffc107,color:#000
    style Done fill:#28a745,color:#fff
    style R403 fill:#dc3545,color:#fff
```

---

## 6. Compromise Impact on Encrypted Libraries

```mermaid
flowchart TD
    subgraph S3["S3 Compromised"]
        S3a["Encrypted blocks:<br/>need file_key to decrypt"]
        S3b["Block sizes visible<br/>(reveals file size patterns)"]
    end

    subgraph Cass["Cassandra Compromised"]
        C1["magic (PBKDF2):<br/>offline brute-force feasible"]
        C2["magic_strong (Argon2id):<br/>brute-force very hard"]
        C3["random_key (encrypted):<br/>need password to decrypt"]
        C4["salt (32 bytes):<br/>not secret, needed for KDF"]
        C5["File names, sizes, tree:<br/>NOT encrypted, exposed"]
    end

    subgraph App["App Server Compromised"]
        A1["Decrypt sessions in RAM:<br/>all unlocked libs readable"]
        A2["Locked libs: still need password"]
    end

    style S3a fill:#28a745,color:#fff
    style C1 fill:#dc3545,color:#fff
    style C2 fill:#28a745,color:#fff
    style C5 fill:#dc3545,color:#fff
    style A1 fill:#dc3545,color:#fff
    style A2 fill:#28a745,color:#fff
```
