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
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
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


def shell_block(body):
    """The `run: |` script of one step, dedented, exactly as the workflow has it."""
    lines = body.splitlines()
    for position, line in enumerate(lines):
        if line.strip() == "run: |":
            block = []
            for rest in lines[position + 1:]:
                if rest.strip() and not rest.startswith(" " * 10):
                    break
                block.append(rest[10:])
            return "\n".join(block).rstrip() + "\n"
    raise AssertionError("step has no run block")


def substitute(block, values):
    """Replace Actions expressions with fixture values, failing on any unknown one.

    Unknown expressions are an error rather than a blank: a new one appearing
    in these blocks is precisely when this harness must stop being trusted.
    """
    def replace(match):
        key = match.group(1).strip()
        if key not in values:
            raise AssertionError(f"unmapped workflow expression {key!r}; the harness is stale")
        return values[key]
    return re.sub(r"\$\{\{([^}]+?)\}\}", replace, block)


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
        # The job's own outputs block sits before the first step, so the scan
        # above cannot see it — and it is what native-bundle consumes.
        declared = self.publish.split("steps:", 1)[0]
        self.assertIn("outputs:", declared)
        self.assertNotIn("steps.hub.outputs.digest", declared)
        self.assertNotIn("steps.dashboard.outputs.digest", declared)
        self.assertIn("steps.refs.outputs.hub", declared)
        self.assertIn("steps.refs.outputs.dashboard", declared)

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



HUB_IMAGE = "ghcr.io/bokiko/bloxos-hub"
DASHBOARD_IMAGE = "ghcr.io/bokiko/bloxos-dashboard"
BUILT_HUB = "sha256:" + "1" * 64
BUILT_DASHBOARD = "sha256:" + "2" * 64
# Deliberately different from the built pair, so a block that silently falls
# back to a rebuild cannot produce these by accident.
RECORDED_HUB = f"{HUB_IMAGE}@sha256:" + "a" * 64
RECORDED_DASHBOARD = f"{DASHBOARD_IMAGE}@sha256:" + "b" * 64


class ReleaseRetryExecutionTests(unittest.TestCase):
    """Run the ACTUAL resolve and promote shell, with a fake docker.

    The structural guards prove no step *mentions* a build digest. They cannot
    prove the reuse path produces the recorded pair and promotes exactly it —
    and that is the path that only ever runs on a retry, where a skipped
    build's output is the empty string.
    """

    @classmethod
    def setUpClass(cls):
        text = WORKFLOW.read_text()
        publish = steps(jobs(text)["publish"])
        by_name = {name: body for name, body in publish}
        cls.resolve = shell_block(next(b for n, b in by_name.items() if "Resolve the image pair" in n))
        cls.promote = shell_block(next(b for n, b in by_name.items() if "Promote both images" in n))

    def setUp(self):
        self.work = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.work, True)
        self.log = self.work / "docker.log"
        binaries = self.work / "bin"
        binaries.mkdir()
        fake = binaries / "docker"
        fake.write_text(
            "#!/bin/sh\n"
            f'printf "%s\\n" "$*" >> {self.log}\n'
            'if [ -n "$FAIL_ON" ]; then case "$*" in *"$FAIL_ON"*) exit 1;; esac; fi\n'
            "exit 0\n")
        fake.chmod(0o755)
        self.path = str(binaries) + os.pathsep + os.environ["PATH"]

    def run_block(self, block, values, fail_on=None, env=None):
        script = substitute(block, values)
        output = self.work / f"output-{len(list(self.work.glob('output-*')))}"
        output.write_text("")
        environment = dict(os.environ, PATH=self.path, GITHUB_OUTPUT=str(output),
                           HUB_IMAGE=HUB_IMAGE, DASHBOARD_IMAGE=DASHBOARD_IMAGE)
        # The step's own `env:` block. The promotion script reads its refs from
        # there rather than inline, which is exactly how the workflow has it.
        environment.update(env or {})
        if fail_on:
            environment["FAIL_ON"] = fail_on
        result = subprocess.run(["bash", "-c", script], text=True, capture_output=True,
                                env=environment, cwd=str(self.work))
        emitted = dict(line.split("=", 1) for line in output.read_text().splitlines() if "=" in line)
        return result, emitted

    def docker_calls(self):
        return self.log.read_text().splitlines() if self.log.exists() else []

    def image_builds(self):
        """Actual image builds, not `buildx imagetools`, which only moves tags."""
        return [call for call in self.docker_calls()
                if call.startswith("build ") or call.startswith("buildx build")]

    def resolve_values(self, reuse):
        """A reuse run has NO build outputs: the steps were skipped, so Actions
        substitutes the empty string. That is the whole hazard."""
        if reuse:
            return {"steps.intent.outputs.reuse": "true",
                    "steps.intent.outputs.hub": RECORDED_HUB,
                    "steps.intent.outputs.dashboard": RECORDED_DASHBOARD,
                    "steps.hub.outputs.digest": "",
                    "steps.dashboard.outputs.digest": ""}
        return {"steps.intent.outputs.reuse": "false",
                "steps.intent.outputs.hub": "",
                "steps.intent.outputs.dashboard": "",
                "steps.hub.outputs.digest": BUILT_HUB,
                "steps.dashboard.outputs.digest": BUILT_DASHBOARD}

    def promote_with(self, hub, dashboard, version="v1.2.3", fail_on=None):
        return self.run_block(self.promote, {}, fail_on=fail_on,
                              env={"VERSION": version, "HUB_REF": hub, "DASHBOARD_REF": dashboard})

    def test_a_fresh_run_resolves_and_promotes_the_freshly_built_pair(self):
        result, emitted = self.run_block(self.resolve, self.resolve_values(reuse=False))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(emitted["hub"], f"{HUB_IMAGE}@{BUILT_HUB}")
        self.assertEqual(emitted["dashboard"], f"{DASHBOARD_IMAGE}@{BUILT_DASHBOARD}")

        result, _ = self.promote_with(emitted["hub"], emitted["dashboard"])
        self.assertEqual(result.returncode, 0, result.stderr)
        created = [call for call in self.docker_calls() if "imagetools create" in call]
        self.assertEqual(len(created), 2, created)
        self.assertTrue(created[0].endswith(f"{HUB_IMAGE}@{BUILT_HUB}"), created[0])
        self.assertTrue(created[1].endswith(f"{DASHBOARD_IMAGE}@{BUILT_DASHBOARD}"), created[1])

    def test_a_reuse_run_promotes_exactly_the_recorded_pair(self):
        result, emitted = self.run_block(self.resolve, self.resolve_values(reuse=True))
        self.assertEqual(result.returncode, 0, result.stderr)
        # Not "@" with nothing after it, and not the built pair: exactly the
        # refs the earlier attempt verified.
        self.assertEqual(emitted["hub"], RECORDED_HUB)
        self.assertEqual(emitted["dashboard"], RECORDED_DASHBOARD)

        result, _ = self.promote_with(emitted["hub"], emitted["dashboard"])
        self.assertEqual(result.returncode, 0, result.stderr)
        created = [call for call in self.docker_calls() if "imagetools create" in call]
        self.assertEqual(len(created), 2, created)
        self.assertTrue(created[0].endswith(RECORDED_HUB), created[0])
        self.assertTrue(created[1].endswith(RECORDED_DASHBOARD), created[1])
        # And nothing was built: the fake docker was never asked to.
        self.assertEqual(self.image_builds(), [])

    def test_a_crash_after_the_first_promotion_retries_onto_the_same_pair(self):
        """The unrecoverable case if the pair were not recorded.

        The hub tag is already live when the run dies. A retry that rebuilt
        would promote a second, different pair over a catalog that can never be
        rewritten to match.
        """
        first_resolve, first = self.run_block(self.resolve, self.resolve_values(reuse=True))
        self.assertEqual(first_resolve.returncode, 0, first_resolve.stderr)
        crashed, _ = self.promote_with(first["hub"], first["dashboard"],
                                       fail_on=DASHBOARD_IMAGE + "@")
        self.assertNotEqual(crashed.returncode, 0, "the injected failure did not fire")
        promoted_before = [c for c in self.docker_calls() if "imagetools create" in c]
        self.assertTrue(any(RECORDED_HUB in c for c in promoted_before),
                        "the hub tag should already be live when the retry starts")

        # The retry: same saved record, same resolution, same promotion.
        self.log.unlink()
        retry_resolve, second = self.run_block(self.resolve, self.resolve_values(reuse=True))
        self.assertEqual(retry_resolve.returncode, 0, retry_resolve.stderr)
        self.assertEqual((second["hub"], second["dashboard"]), (first["hub"], first["dashboard"]))
        result, _ = self.promote_with(second["hub"], second["dashboard"])
        self.assertEqual(result.returncode, 0, result.stderr)
        created = [c for c in self.docker_calls() if "imagetools create" in c]
        self.assertTrue(any(c.endswith(RECORDED_HUB) for c in created), created)
        self.assertTrue(any(c.endswith(RECORDED_DASHBOARD) for c in created), created)
        self.assertEqual(self.image_builds(), [])

    def test_the_resolver_refuses_a_reference_that_lost_its_digest(self):
        """CONTROL for the harness: an empty build output must be caught here,
        not handed to docker."""
        broken = self.resolve_values(reuse=False)
        broken["steps.hub.outputs.digest"] = ""
        result, emitted = self.run_block(self.resolve, broken)
        self.assertNotEqual(result.returncode, 0,
                            "a digest-less reference must not reach promotion")
        self.assertNotIn("hub", emitted)


if __name__ == "__main__":
    unittest.main(verbosity=2)
