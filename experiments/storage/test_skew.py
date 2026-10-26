import importlib.util, pathlib, tempfile, threading, unittest

spec=importlib.util.spec_from_file_location('skew',pathlib.Path(__file__).with_name('skew.py'))
skew=importlib.util.module_from_spec(spec); spec.loader.exec_module(skew)

class SkewChecks(unittest.TestCase):
    def test_anchor_rejects_changed_hash(self):
        with tempfile.TemporaryDirectory() as directory:
            path=pathlib.Path(directory)/'headers.bin'; header=bytes(range(80)); path.write_bytes(header)
            item={'height':0,'block_hash':skew.replay.dhash(header)[::-1].hex()}
            skew.anchor(path,item)
            with self.assertRaises(AssertionError): skew.anchor(path,dict(item,block_hash='00'*32))
    def test_broker_reuses_and_releases_one_bounded_slot(self):
        b=skew.Broker([],pathlib.Path('.'),pathlib.Path('.'),max_bodies=1,max_estimated_bytes=2)
        b._load=lambda item: ((item['height'],item['block_hash'],[],[]),1)
        one={'height':1,'block_hash':'a'}; two={'height':2,'block_hash':'b'}
        started=threading.Event(); obtained=threading.Event()
        def second():
            started.set()
            with b.borrow(two): obtained.set()
        with b.borrow(one):
            with b.borrow(one): self.assertEqual(b.hits,1)
            t=threading.Thread(target=second); t.start(); self.assertTrue(started.wait(2))
            self.assertFalse(obtained.wait(.05)); self.assertEqual(b.peak_bodies,1)
        self.assertTrue(obtained.wait(2)); t.join(2)
        self.assertFalse(t.is_alive()); self.assertEqual(b.bytes,0); self.assertFalse(b.active)
        self.assertEqual(b.loads,2); self.assertGreaterEqual(b.waits,1)

    def test_retained_hit_and_byte_eviction(self):
        b=skew.Broker([],pathlib.Path('.'),pathlib.Path('.'),max_bodies=1,max_estimated_bytes=2,max_retained_blocks=2,max_retained_bytes=1)
        b._load=lambda item: ((item['height'],item['block_hash'],[],[]),1)
        one={'height':1,'block_hash':'a'}; two={'height':2,'block_hash':'b'}
        with b.borrow(one): pass
        with b.borrow(one): pass
        self.assertEqual((b.loads,b.retained_hits,b.retained_bytes),(1,1,1))
        with self.assertRaises(AssertionError):
            with b.borrow({'height':1,'block_hash':'different'}): pass
        with b.borrow(two): pass
        self.assertEqual((b.loads,b.evictions,b.retained_bytes),(2,1,1))
        with b.borrow(one): pass
        self.assertEqual((b.loads,b.evictions,b.retained_bytes),(3,2,1))
        self.assertEqual(b.peak_combined_bytes,2)

if __name__=='__main__': unittest.main()
