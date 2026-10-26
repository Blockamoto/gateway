import importlib.util, pathlib, unittest
spec=importlib.util.spec_from_file_location('baseline_078',pathlib.Path(__file__).with_name('baseline_078.py'))
b=importlib.util.module_from_spec(spec); spec.loader.exec_module(b)
class OwnershipChecks(unittest.TestCase):
    def test_owned_child_and_reject_outside_and_build_root(self):
        self.assertEqual(b.owned(b.BUILD/'new-owned-profile'),(b.BUILD/'new-owned-profile').resolve())
        for p in (b.BUILD,b.ROOT/'not-build',b.BUILD/'..'/'outside'):
            with self.subTest(p=p), self.assertRaises(AssertionError): b.owned(p)
if __name__=='__main__': unittest.main()
