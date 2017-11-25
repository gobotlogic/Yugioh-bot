import datetime
import logging
import threading
import time

import apscheduler
import os

from bot.providers import trainer_matches as tm
from bot.providers.duellinks import DuelLinks
from bot.providers.misc import Misc
from bot.providers.actions import Actions


class Provider(DuelLinks, Misc, Actions):
    scheduler = None
    current_job = None  # indicates the current job running
    lock = None
    # indicates that this provider is currently battling
    current_battle = None
    # logger
    root = logging.getLogger("bot.provider")
    assets = None

    def setUp(self):
        pass

    def __init__(self, scheduler, config):
        self.scheduler = scheduler
        self._config = config
        self.assets = config.get('locations', 'assets')
        self.lock = threading.Lock()
        self.setUp()

    def auto(self):
        t = threading.currentThread()
        self.register_thread(t)
        for x in range(0, 8):
            if not getattr(t, "do_run", True):
                # Leaves a checkpoint when stopped
                self.current_run(x)
                break
            self.compare_with_back_button()
            self.wait_for_ui(1)
            self.swipe_right()
            self.scan()
        self.register_thread(None)

    def debug_battle(self):
        self.battle(CheckBattle=self.check_battle)
        # self.CheckBattle()

    def __check_battle_is_running__(self):
        self.root.debug("CHECKING AUTO DUEL STATUS")
        img = self.get_img_from_screen_shot()
        status = self.determine_autoduel_status(img)
        self.root.debug("AUTO_DUEL STATUS: {}".format(status))
        if not status and self.current_battle:
            self.click_auto_duel()
            self.check_battle()

    def check_battle(self, signal_done=False, delay=5):
        self.lock.acquire()
        try:
            self.scheduler.remove_job(self.current_job)
        except apscheduler.jobstores.base.JobLookupError:
            if signal_done:
                self.current_battle = False
                self.lock.release()
                return
        when = datetime.datetime.now() + datetime.timedelta(seconds=delay)
        job_id = 'cron_check_battle_at_%s' % (when.isoformat())
        self.current_job = job_id
        self.scheduler.add_job(self.__check_battle_is_running__, trigger='date',
                               id=job_id, run_date=when)
        self.lock.release()
        self.root.debug(job_id)

    def scan(self):
        raise NotImplementedError("scan not implemented")

    def possible_battle_points(self):
        img = self.get_img_from_screen_shot()
        t = tm.Trainer(img)
        t.whiteCircles()
        current_page = self.get_current_page(img)
        for x, y in t.circlePoints:
            yield x, y, current_page

    def wait_for_auto_duel(self):
        self.root.debug("WAITING FOR AUTO-DUEL TO APPEAR")
        word = ''
        while 'Auto-Duel' not in word and 'AutoDuel' not in word:
            img = self.get_img_from_screen_shot()
            area = img.crop(self.auto_duel_box)
            try:
                word = Provider.img_to_string(area, "Auto-Duel")
            except:
                self.wait_for_ui(1)
                continue
            self.wait_for_ui(.5)
        self.click_auto_duel()

    @staticmethod
    def img_to_string(img, char_set=None):
        img.save("tmp\\ocr.png")
        Command = "bin\\tess\\tesseract.exe --tessdata-dir bin\\tess\\tessdata tmp\\ocr.png tmp\\ocr "
        if char_set != None:
            Command += "-c tessedit_char_whitelist=" + char_set + " "
        Command += "-psm 7 "
        Command += "> nul 2>&1"
        os.system(Command)
        # Get the largest line in txt
        with open("tmp\\ocr.txt") as f:
            content = f.read().splitlines()
        OutputLine = ""
        for line in content:
            line = line.strip()
            if len(line) > len(OutputLine):
                OutputLine = line
        return OutputLine
