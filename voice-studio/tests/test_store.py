from kova_voice_studio.store import ProfileStore


def test_profile_requires_consent_and_creates_immutable_first_version(tmp_path):
    store = ProfileStore(tmp_path / "voice.db")
    digest = "a" * 64
    try:
        try:
            store.create_profile(
                name="Giọng thử", language="vi", reference_filename="sample.wav", reference_sha256=digest, consent=False
            )
        except ValueError as error:
            assert "consent" in str(error)
        else:
            raise AssertionError("consent must be required")

        profile, version = store.create_profile(
            name="Giọng thử", language="vi", reference_filename="sample.wav", reference_sha256=digest, consent=True
        )
        assert version.profile_id == profile.id
        assert version.version == 1
        assert store.latest_version(profile.id) == version
    finally:
        store.close()
