import json
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch
from urllib.request import Request, urlopen

from PIL import Image


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "src"))

from webapp import PictogrepServer, board_records, existing_app_url, safe_upload_name, unique_path


class WebAppHelpersTest(unittest.TestCase):
    def test_safe_upload_name_removes_paths_and_unsafe_characters(self):
        self.assertEqual(safe_upload_name("../../My odd:image.PNG"), "My-odd-image.png")

    def test_safe_upload_name_rejects_non_images(self):
        with self.assertRaises(ValueError):
            safe_upload_name("notes.txt")

    def test_unique_path_preserves_existing_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp)
            (folder / "still.png").touch()
            self.assertEqual(unique_path(folder, "still.png").name, "still-2.png")

    def test_board_records_reads_sidecar_and_sorts_newest_first(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp)
            first = folder / "first.png"
            second = folder / "second.png"
            Image.new("RGB", (20, 10), "white").save(first)
            Image.new("RGB", (20, 10), "white").save(second)
            second.with_suffix(".json").write_text(json.dumps({"query": "fog", "aspect": "16:9"}))
            first.touch()
            boards = board_records(folder)
            self.assertEqual(boards[0]["name"], "first.png")
            self.assertEqual(boards[1]["query"], "fog")


class WebAppHttpTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        root = Path(self.temp.name)
        self.picture = root / "reference.png"
        Image.new("RGB", (32, 24), "#777777").save(self.picture)
        self.out = root / "boards"
        self.out.mkdir()
        Image.new("RGB", (32, 24), "white").save(self.out / "board.png")
        self.server = PictogrepServer(("127.0.0.1", 0), [self.picture], self.out)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.temp.cleanup()

    def get(self, path):
        with urlopen(self.base + path) as response:
            return response.status, response.headers.get_content_type(), response.read()

    def post_json(self, path, data):
        request = Request(
            self.base + path,
            data=json.dumps(data).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urlopen(request) as response:
            return response.status, json.loads(response.read())

    def test_dashboard_and_assets_are_served(self):
        status, content_type, body = self.get("/")
        self.assertEqual(status, 200)
        self.assertEqual(content_type, "text/html")
        self.assertIn(b"Search pictures", body)
        status, content_type, body = self.get("/assets/app.js")
        self.assertEqual(status, 200)
        self.assertIn("javascript", content_type)
        self.assertIn(b"loadImages", body)

    def test_library_returns_browser_image_records(self):
        _, _, body = self.get("/api/app/images?count=5")
        data = json.loads(body)
        self.assertEqual(data["total"], 1)
        self.assertEqual(data["images"][0]["name"], "reference.png")
        self.assertEqual(data["images"][0]["width"], 32)
        self.assertEqual(data["images"][0]["height"], 24)
        _, content_type, image = self.get(data["images"][0]["url"])
        self.assertEqual(content_type, "image/png")
        self.assertTrue(image.startswith(b"\x89PNG"))

    def test_practice_can_start_with_one_selected_image(self):
        _, _, body = self.get("/api/images?image=0")
        data = json.loads(body)
        self.assertEqual(data["selected"], 1)
        self.assertEqual(data["images"][0]["name"], "reference.png")

    def test_saved_boards_are_listed_and_served(self):
        _, _, body = self.get("/api/app/boards")
        data = json.loads(body)
        self.assertEqual([board["name"] for board in data["boards"]], ["board.png"])
        _, content_type, image = self.get(data["boards"][0]["url"])
        self.assertEqual(content_type, "image/png")
        self.assertTrue(image.startswith(b"\x89PNG"))

    def test_static_assets_support_head_requests(self):
        request = Request(self.base + "/assets/app.css", method="HEAD")
        with urlopen(request) as response:
            self.assertEqual(response.status, 200)
            self.assertEqual(response.headers.get_content_type(), "text/css")

    def test_running_app_can_be_reused_by_the_launcher(self):
        self.assertEqual(existing_app_url(self.server.server_port), self.base + "/")
        self.assertEqual(existing_app_url(self.server.server_port, "/practice"), self.base + "/practice")

    def test_folder_can_be_filled_from_a_local_clip_prompt(self):
        folder = self.out / "cats"
        folder.mkdir()
        matches = [{"path": str(self.picture), "score": 0.8}]
        with (
            patch("webapp.create_collection", return_value=folder),
            patch("webapp.clip_search", return_value=matches) as search,
            patch("webapp.link_image", return_value=True) as link,
        ):
            status, data = self.post_json(
                "/api/app/tags",
                {"action": "fill", "tag": "cats", "prompt": "soft cats", "limit": 25},
            )
        self.assertEqual(status, 200)
        self.assertEqual(data["added"], 1)
        search.assert_called_once_with("soft cats", limit=25)
        link.assert_called_once_with(folder, str(self.picture))


if __name__ == "__main__":
    unittest.main()
