import { useState } from 'preact/hooks';

export function useToast() {
  const [msg, setMsg] = useState('');
  const [visible, setVisible] = useState(false);

  function toast(message: string, duration = 2200) {
    setMsg(message);
    setVisible(true);
    setTimeout(() => setVisible(false), duration);
  }

  return { msg, visible, toast };
}
