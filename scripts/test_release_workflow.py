#!/usr/bin/env python3
"""Wiring checks for the release workflow. No network, no Docker, no YAML library.

Unit-testing the intent document proves it round-trips. It does NOT prove the
workflow uses it: a reuse path that resolves the recorded pair and then promotes
`steps.hub.outputs.digest` anyway is inert on the happy path and catastrophic on
the one run that matters, because a skipped build's output is the empty string.

These assertions are deliberately textual. The properties are textual — which
expression a step references, and what order the steps are in — and a YAML
library is not installed everywhere this runs.
"""
from pathlib import Path
import re
import sys
import unittest

WORKFLOW = Path(__file__).resolve().parent.parent / ".github" / "workflows" / "docker.yml"


def jobs(text):
    """Split the workflow into {job name: body}, by indentation."""
    found, name, start = {}, None, None
    lines = text.splitlines(keepends=True)
    offset = 0
    for line in lines:
        match = re.fullmatch(r"  ([A-Za-z][\w-]*):\n", line)
        if match:
            if name is not None:
                found[name] = text[start:offset]
            name, start = match.group(1), offset
        offset += len(line)
    if name is not None:
        found[name] = text[start:]
    return found


def steps(job):
    """Split one job's body into its steps, in order, as (name, body)."""
    pieces = re.split(r"\n      - ", "\n" + job)
    out = []
    for piece in pieces[1:]:
        match = re.search(r"name: (.+)", piece)
        out.append(((match.group(1).strip() if match else piece.splitlines()[0].strip()), piece))
    return out


class ReleaseWorkflowWiringTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = WORKFLOW.read_text()
        cls.publish = jobs(cls.text)["publish"]
        cls.native = jobs(cls.text)["native-bundle"]
        cls.publish_steps = steps(cls.publish)

    def index_of(self, fragment, where=None):
        for position, (name, _) in enumerate(where or self.publish_steps):
            if fragment.lower() in name.lower():
                return position
        self.fail(f"no step matching {fragment!r}; the parser or the workflow has moved")

    def body_of(self, fragment):
        return self.publish_steps[self.index_of(fragment)][1]

    # CONTROL: if this parser silently found nothing, every assertion below
    # would pass vacuously.
    def test_the_parser_actually_sees_the_publish_job(self):
        self.assertGreater(len(self.publish_steps), 8,
                           "the publish job was not parsed into steps")
        for required in ("Look up a verified image pair", "Push hub image",
                         "Push dashboard image", "Resolve the image pair", "Promote both images"):
            self.index_of(required)

    def test_only_the_resolve_step_reads_a_build_digest(self):
        """A skipped build's output is the empty string.

        Any surviving reference would resolve to `ghcr.io/...@` on a retry —
        an invalid reference at best, and at worst a tag promoted to something
        nothing verified.
        """
        offenders = [name for name, body in self.publish_steps
                     if "outputs.digest" in body and "Resolve the image pair" not in name]
        self.assertEqual(offenders, [],
                         f"these steps still read a build digest directly: {offenders}")
        self.assertNotIn("outputs.digest", self.native,
                         "the native bundle job must consume the resolved refs, not build outputs")

    def test_both_builds_are_skipped_when_a_verified_pair_is_reused(self):
        for image in ("Push hub image", "Push dashboard image"):
            with self.subTest(image=image):
                self.assertIn("if: steps.intent.outputs.reuse != 'true'", self.body_of(image),
                              f"{image} would rebuild over a pair that was already verified")

    def test_the_lookup_happens_before_anything_is_built(self):
        lookup = self.index_of("Look up a verified image pair")
        self.assertLess(lookup, self.index_of("Push hub image"))
        self.assertLess(lookup, self.index_of("Push dashboard image"))

    def test_both_refs_flow_through_promotion_and_native_extraction(self):
        promote = self.body_of("Promote both images")
        self.assertIn("steps.refs.outputs.hub", promote)
        self.assertIn("steps.refs.outputs.dashboard", promote)
        # Recovering only the hub would leave the dashboard to be rebuilt, and
        # the pair would no longer be the pair anything was verified against.
        self.assertIn("needs.publish.outputs.hub_ref", self.native)
        self.assertIn("needs.publish.outputs.dashboard_ref", self.native)

    def test_both_images_are_validated_on_both_architectures_before_anything_moves(self):
        validate = self.index_of("Validate the platform and source of both images")
        body = self.publish_steps[validate][1]
        self.assertIn("steps.refs.outputs.hub", body)
        self.assertIn("steps.refs.outputs.dashboard", body)
        self.assertIn("linux/amd64 linux/arm64", body)
        self.assertIn("--revision", body)
        # A reused record matching this run's sha is evidence about the record,
        # not about the images it names — so this must precede recording it.
        self.assertLess(validate, self.index_of("Persist the verified image pair"))
        self.assertLess(validate, self.index_of("Promote both images"))

    def test_the_pair_is_recorded_after_verification_and_before_the_catalog(self):
        record = self.index_of("Persist the verified image pair")
        self.assertLess(self.index_of("Verify release identity"), record)
        self.assertLess(record, self.index_of("Persist the verified agent catalog"))
        self.assertLess(record, self.index_of("Promote both images"))

    def test_no_step_treats_a_failed_release_lookup_as_absence(self):
        """`gh release view ... || gh release create ...` reads ANY failure as
        "no release exists" — an expired token would create a release that is
        already published, and skip the assertion that it is still a draft."""
        for job in ("publish", "native-bundle"):
            with self.subTest(job=job):
                body = jobs(self.text)[job]
                self.assertNotIn("gh release view", re.sub(r"^\s*#.*$", "", body, flags=re.M),
                                 f"{job} still branches on a failed release lookup")
                self.assertIn("release_history.py ensure-draft", body)


if __name__ == "__main__":
    unittest.main(verbosity=2)
