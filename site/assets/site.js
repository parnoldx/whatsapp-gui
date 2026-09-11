document.addEventListener('DOMContentLoaded', () => {
  // 1. Copy Buttons
  const copyButtons = document.querySelectorAll('[data-copy]');
  copyButtons.forEach((btn) => {
    btn.removeAttribute('hidden');
    btn.addEventListener('click', async () => {
      const targetId = btn.getAttribute('data-copy');
      const targetEl = document.getElementById(targetId);
      if (!targetEl) return;

      const textToCopy = targetEl.innerText.trim();
      try {
        await navigator.clipboard.writeText(textToCopy);
        const originalText = btn.innerText;
        btn.innerText = 'Copied!';
        btn.classList.add('copied');
        setTimeout(() => {
          btn.innerText = originalText;
          btn.classList.remove('copied');
        }, 2000);
      } catch (err) {
        console.error('Failed to copy', err);
      }
    });
  });

  // 2. Inline Social Video Preview Simulation in Blade Mockup
  const reelCard = document.getElementById('mockReelCard');
  const inWindowPlayer = document.getElementById('mockInWindowPlayer');
  const closePlayerBtn = document.getElementById('mockClosePlayerBtn');

  if (reelCard && inWindowPlayer && closePlayerBtn) {
    reelCard.addEventListener('click', () => {
      inWindowPlayer.hidden = false;
    });

    closePlayerBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      inWindowPlayer.hidden = true;
    });

    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && !inWindowPlayer.hidden) {
        inWindowPlayer.hidden = true;
      }
    });
  }

  // 3. Voice Note Mock Player
  const audioPlayBtn = document.getElementById('audioPlayBtn');
  const audioDuration = document.getElementById('audioDuration');
  let isPlaying = false;
  let playInterval = null;
  let seconds = 0;

  if (audioPlayBtn && audioDuration) {
    audioPlayBtn.addEventListener('click', () => {
      isPlaying = !isPlaying;
      audioPlayBtn.innerText = isPlaying ? '⏸' : '▶';

      if (isPlaying) {
        playInterval = setInterval(() => {
          seconds++;
          if (seconds > 42) {
            seconds = 0;
            isPlaying = false;
            audioPlayBtn.innerText = '▶';
            clearInterval(playInterval);
          }
          const mins = Math.floor(seconds / 60);
          const secs = (seconds % 60).toString().padStart(2, '0');
          audioDuration.innerText = `${mins}:${secs} / 0:42`;
        }, 1000);
      } else {
        clearInterval(playInterval);
      }
    });
  }
});
